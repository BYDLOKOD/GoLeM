// Package prompt provides constraint expansion and system prompt assembly
// for GoLeM subagent invocations.
package prompt

import (
	"fmt"
	"strings"
)

// knownConstraints maps a constraint key to its expanded instruction text.
var knownConstraints = map[string]string{
	"readonly":   "You MUST NOT create, modify, or delete any files. You may only read files and report findings.",
	"no-create":  "You MUST NOT create any new files. You may only read or modify existing files.",
	"plan-first": "Before making any changes, you MUST output a detailed plan of what you intend to do and wait for approval.",
}

// ExpandConstraint maps a constraint key to its full instruction text.
//
// Supported keys:
//   - "readonly"   — prohibits all file writes and deletes
//   - "no-create"  — prohibits creating new files
//   - "plan-first" — requires a plan before any change
//   - "scope:<path>" — restricts operations to the given directory path
//
// Returns an error for unknown keys or a "scope:" key with an empty path.
func ExpandConstraint(key string) (string, error) {
	// Handle scope: prefix first.
	if strings.HasPrefix(key, "scope:") {
		path := strings.TrimPrefix(key, "scope:")
		if path == "" {
			return "", fmt.Errorf(`err:user "scope: constraint requires a non-empty path"`)
		}
		return fmt.Sprintf(
			"You MUST only operate on files under the path: %s. Do not read or modify any files outside this directory.",
			path,
		), nil
	}

	if text, ok := knownConstraints[key]; ok {
		return text, nil
	}

	return "", fmt.Errorf(`err:user "unknown constraint %q"`, key)
}

// AssembleSystemPrompt validates and expands constraints, then assembles a
// final system prompt string.
//
// Assembly rules:
//   - constraints are expanded and joined with "\n\n"
//   - systemPrompt is trimmed of leading/trailing whitespace
//   - when both are present: expanded constraints + "\n\n" + trimmed prompt
//   - when only constraints: joined constraint texts
//   - when only systemPrompt: trimmed prompt
//   - when neither: empty string
//
// Returns an error if any constraint key is unknown.
func AssembleSystemPrompt(constraints []string, systemPrompt string) (string, error) {
	var parts []string

	for _, key := range constraints {
		text, err := ExpandConstraint(key)
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}

	trimmed := strings.TrimSpace(systemPrompt)

	if len(parts) == 0 {
		return trimmed, nil
	}

	constraintBlock := strings.Join(parts, "\n\n")
	if trimmed == "" {
		return constraintBlock, nil
	}

	return constraintBlock + "\n\n" + trimmed, nil
}
