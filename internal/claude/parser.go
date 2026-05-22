package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rawOutput is the legacy single-object form of claude --output-format json.
type rawOutput struct {
	Result   string       `json:"result"`
	Messages []rawMessage `json:"messages"`
}

// streamEvent is one element of the array form emitted by current Claude Code
// (claude 2.1+): [{type:"system"...},{type:"assistant",message:{...}},{type:"result",result:"..."}].
// The final result text lives on the type=="result" element; tool_use blocks
// live under the message.content of type=="assistant" elements.
type streamEvent struct {
	Type    string     `json:"type"`
	Result  string     `json:"result"`
	Message rawMessage `json:"message"`
}

type rawMessage struct {
	Role    string       `json:"role"`
	Content []rawContent `json:"content"`
}

type rawContent struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// editInput is the decoded shape of a Edit tool_use input.
type editInput struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// writeInput is the decoded shape of a Write tool_use input.
type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// bashInput is the decoded shape of a Bash tool_use input.
type bashInput struct {
	Command string `json:"command"`
}

// notebookInput is the decoded shape of a NotebookEdit tool_use input.
type notebookInput struct {
	NotebookPath string `json:"notebook_path"`
}

// ParseRawJSON reads raw.json from jobDir, extracts the ".result" field into
// stdout.txt, and calls GenerateChangelog to produce changelog.txt.
//
// Errors (malformed JSON, missing fields) are handled gracefully: stdout.txt
// and changelog.txt are always written; a warning is logged to stderr.
func ParseRawJSON(jobDir string) error {
	rawPath := filepath.Join(jobDir, "raw.json")
	data, err := os.ReadFile(rawPath)
	if err != nil {
		return fmt.Errorf("read raw.json: %w", err)
	}

	result, toolUses, perr := extractResult(data)
	if perr != nil {
		// Malformed JSON - warn and write empty files.
		fmt.Fprintf(os.Stderr, "warning: malformed JSON in raw.json: %v\n", perr)
		if writeErr := os.WriteFile(filepath.Join(jobDir, "stdout.txt"), []byte(""), 0o644); writeErr != nil {
			return writeErr
		}
		return GenerateChangelog(jobDir, nil)
	}

	// Write stdout.txt from the result text.
	if err := os.WriteFile(filepath.Join(jobDir, "stdout.txt"), []byte(result), 0o644); err != nil {
		return fmt.Errorf("write stdout.txt: %w", err)
	}

	return GenerateChangelog(jobDir, toolUses)
}

// extractResult parses claude's --output-format json output into the final
// result text and the tool_use content blocks. It supports both the current
// array form (a stream of typed events) and the legacy single-object form, so
// a Claude Code update that flips between them does not silently blank the
// golem's output.
func extractResult(data []byte) (string, []rawContent, error) {
	trimmed := bytes.TrimSpace(data)

	if len(trimmed) > 0 && trimmed[0] == '[' {
		var events []streamEvent
		if err := json.Unmarshal(trimmed, &events); err != nil {
			return "", nil, err
		}
		var result string
		var toolUses []rawContent
		for _, e := range events {
			if e.Type == "result" {
				result = e.Result
			}
			for _, c := range e.Message.Content {
				if c.Type == "tool_use" {
					toolUses = append(toolUses, c)
				}
			}
		}
		return result, toolUses, nil
	}

	var out rawOutput
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return "", nil, err
	}
	var toolUses []rawContent
	for _, msg := range out.Messages {
		for _, c := range msg.Content {
			if c.Type == "tool_use" {
				toolUses = append(toolUses, c)
			}
		}
	}
	return out.Result, toolUses, nil
}

// GenerateChangelog synthesises changelog.txt from a slice of tool_use content
// blocks.  When toolUses is empty or nil it writes "(no file changes)".
func GenerateChangelog(jobDir string, toolUses []rawContent) error {
	var lines []string

	for _, tu := range toolUses {
		switch tu.Name {
		case "Edit":
			var inp editInput
			if err := json.Unmarshal(tu.Input, &inp); err != nil {
				continue
			}
			charCount := len(inp.NewString)
			lines = append(lines, fmt.Sprintf("EDIT %s: %d chars", inp.FilePath, charCount))

		case "Write":
			var inp writeInput
			if err := json.Unmarshal(tu.Input, &inp); err != nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("WRITE %s", inp.FilePath))

		case "Bash":
			var inp bashInput
			if err := json.Unmarshal(tu.Input, &inp); err != nil {
				continue
			}
			cmd := inp.Command
			if len(cmd) > 80 {
				cmd = cmd[:80]
			}
			if isDeleteCommand(cmd) {
				lines = append(lines, fmt.Sprintf("DELETE via bash: %s", cmd))
			} else if !isCompoundCommand(cmd) {
				lines = append(lines, fmt.Sprintf("FS: %s", cmd))
			}

		case "NotebookEdit":
			var inp notebookInput
			if err := json.Unmarshal(tu.Input, &inp); err != nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("NOTEBOOK %s", inp.NotebookPath))
		}
	}

	var content string
	if len(lines) == 0 {
		content = "(no file changes)"
	} else {
		content = strings.Join(lines, "\n")
	}

	return os.WriteFile(filepath.Join(jobDir, "changelog.txt"), []byte(content), 0o644)
}

// isDeleteCommand reports whether a bash command is a delete/remove operation.
func isDeleteCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	return strings.HasPrefix(trimmed, "rm ") || strings.HasPrefix(trimmed, "rm\t") || trimmed == "rm"
}

// isCompoundCommand reports whether a bash command is a compound shell expression
// (joined by &&, ||, ;, or a pipe |).  Compound commands like "cd /foo && go vet ./..."
// are read-only orchestration and are excluded from the FS changelog.
func isCompoundCommand(cmd string) bool {
	return strings.Contains(cmd, "&&") ||
		strings.Contains(cmd, "||") ||
		strings.Contains(cmd, ";") ||
		strings.Contains(cmd, "|")
}
