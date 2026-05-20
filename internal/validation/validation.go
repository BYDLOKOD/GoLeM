package validation

import (
	"fmt"
	"regexp"
	"strings"
)

type ValidationError struct {
	Condition string
	Detail    string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed [%s]: %s", e.Condition, e.Detail)
}

type ValidationRule struct {
	Contains    []string `json:"contains,omitempty"`
	NotContains []string `json:"not_contains,omitempty"`
	Matches     string   `json:"matches,omitempty"`
}

func (r *ValidationRule) Check(stdout string) error {
	if r == nil {
		return nil
	}

	for _, s := range r.Contains {
		if !strings.Contains(stdout, s) {
			return &ValidationError{
				Condition: "contains",
				Detail:    fmt.Sprintf("expected %q in output", s),
			}
		}
	}

	for _, s := range r.NotContains {
		if strings.Contains(stdout, s) {
			return &ValidationError{
				Condition: "not_contains",
				Detail:    fmt.Sprintf("forbidden %q found in output", s),
			}
		}
	}

	if r.Matches != "" {
		re, err := regexp.Compile(r.Matches)
		if err != nil {
			return fmt.Errorf("regex compile error: %w", err)
		}
		if !re.MatchString(stdout) {
			return &ValidationError{
				Condition: "matches",
				Detail:    fmt.Sprintf("output does not match pattern %q", r.Matches),
			}
		}
	}

	return nil
}
