// Package router provides prompt complexity estimation for smart model routing.
// It classifies prompts into tiers (Light, Medium, Heavy) based on keyword
// matching, allowing the caller to select an appropriate model for the task.
package router

import "strings"

// Tier represents the complexity classification of a prompt.
type Tier int

const (
	Medium Tier = iota // zero-value is the default: general coding tasks
	Light              // simple tasks: lint, format, rename, fix typo
	Heavy              // complex tasks: refactor, debug, architecture, design
)

// String returns a human-readable name for the tier.
func (t Tier) String() string {
	switch t {
	case Medium:
		return "medium"
	case Light:
		return "light"
	case Heavy:
		return "heavy"
	default:
		return "unknown"
	}
}

// lightKeywords classifies a prompt as Light when any keyword is found
// as a substring (case-insensitive) in the prompt or hints.
var lightKeywords = []string{
	"lint",
	"format",
	"rename",
	"fix typo",
	"add import",
	"whitespace",
	"sort import",
	"organize import",
}

// heavyKeywords classifies a prompt as Heavy when any keyword is found
// as a substring (case-insensitive) in the prompt or hints.
var heavyKeywords = []string{
	"refactor",
	"debug",
	"architecture",
	"design",
	"migrate",
	"rewrite",
	"optimiz",
	"implement new",
	"performance",
}

// Estimate classifies the given prompt (and optional hints) into a Tier.
// It concatenates the prompt and hints, lowercases the result, and checks
// for keyword matches. Heavy keywords are checked first: if any heavy
// keyword matches, the result is Heavy. Then light keywords are checked.
// If nothing matches, the default is Medium.
func Estimate(prompt string, hints ...string) Tier {
	// Combine prompt and hints into one string for matching.
	all := prompt
	for _, h := range hints {
		all += " " + h
	}
	lower := strings.ToLower(all)

	// Check heavy first -- heavy overrides light.
	if containsAny(lower, heavyKeywords) {
		return Heavy
	}

	// Check light.
	if containsAny(lower, lightKeywords) {
		return Light
	}

	// Default: medium.
	return Medium
}

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, substrings []string) bool {
	for _, sub := range substrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
