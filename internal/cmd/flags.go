// Package cmd implements the core CLI commands: run, start, status, result.
package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Flags holds all parsed command-line options for run and start commands.
type Flags struct {
	Dir            string
	Timeout        int
	Model          string
	OpusModel      string
	SonnetModel    string
	HaikuModel     string
	PermissionMode string
	Tier           string // "light", "medium", "heavy", "auto", or "" (auto)
	SystemPrompt   string
	Constraints    []string
	Prompt         string
}

// ParseFlags parses the given argument slice (excluding the subcommand name)
// and returns a populated Flags. It does NOT validate the values.
// The positional arguments remaining after flag processing are joined as the prompt.
func ParseFlags(args []string) (*Flags, error) {
	f := &Flags{
		Dir:     ".",
		Timeout: 0,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch arg {
		case "-d", "--dir":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for %s flag"`, arg)
			}
			f.Dir = args[i+1]
			i++

		case "-t", "--timeout":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for %s flag"`, arg)
			}
			val := args[i+1]
			timeout, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf(`err:user "Timeout must be a positive number: %s"`, val)
			}
			f.Timeout = timeout
			i++

		case "-m", "--model":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for -m flag"`)
			}
			f.Model = args[i+1]
			i++

		case "--opus":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for --opus flag"`)
			}
			f.OpusModel = args[i+1]
			i++

		case "--sonnet":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for --sonnet flag"`)
			}
			f.SonnetModel = args[i+1]
			i++

		case "--haiku":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for --haiku flag"`)
			}
			f.HaikuModel = args[i+1]
			i++

		case "--unsafe":
			f.PermissionMode = "bypassPermissions"

		case "--mode":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for --mode flag"`)
			}
			f.PermissionMode = args[i+1]
			i++

		case "--tier":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for --tier flag"`)
			}
			val := args[i+1]
			validTiers := map[string]bool{"light": true, "medium": true, "heavy": true, "auto": true}
			if !validTiers[val] {
				return nil, fmt.Errorf(`err:user "Invalid --tier value: %s (must be light, medium, heavy, or auto)"`, val)
			}
			f.Tier = val
			i++

		case "--system-prompt":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for --system-prompt flag"`)
			}
			f.SystemPrompt = args[i+1]
			i++

		case "--constraint":
			if i+1 >= len(args) {
				return nil, fmt.Errorf(`err:user "Missing value for --constraint flag"`)
			}
			f.Constraints = append(f.Constraints, args[i+1])
			i++

		default:
			// Positional arguments - collect all remaining args as prompt
			f.Prompt = strings.Join(args[i:], " ")
			return f, nil
		}
	}

	return f, nil
}

// Validate checks the populated Flags for semantic correctness:
//   - Dir must exist on the filesystem (unless it is ".")
//   - Timeout must be a positive integer
//   - Prompt must be non-empty
//
// It returns an error whose message matches the BDD-specified format:
//
//	err:user "Directory not found: <dir>"
//	err:user "Timeout must be a positive number: <val>"
//	err:user "No prompt provided"
func Validate(f *Flags) error {
	// Check prompt is not empty first
	if f.Prompt == "" {
		return fmt.Errorf(`err:user "No prompt provided"`)
	}

	// Check directory exists (unless it's ".")
	if f.Dir != "." {
		if _, err := os.Stat(f.Dir); os.IsNotExist(err) {
			return fmt.Errorf(`err:user "Directory not found: %s"`, f.Dir)
		}
	}

	// Check timeout is positive
	if f.Timeout <= 0 {
		return fmt.Errorf(`err:user "Timeout must be a positive number: %d"`, f.Timeout)
	}

	return nil
}

// DefaultTimeout is used when the caller has not provided a -t flag.
// In production it is read from the config; here it defaults to 0 (invalid)
// so that the "Default timeout comes from config" scenario can be tested.
const DefaultTimeout = 0
