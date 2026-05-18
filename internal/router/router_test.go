package router

import (
	"strings"
	"testing"
)

func TestEstimateLightKeywords(t *testing.T) {
	prompts := []string{
		"lint the code",
		"run the linter",
		"format this file",
		"reformat the output",
		"rename variable foo to bar",
		"fix typo in README",
		"add import for fmt",
		"fix whitespace issues",
		"sort imports",
		"organize imports in main.go",
	}

	for _, prompt := range prompts {
		tier := Estimate(prompt)
		if tier != Light {
			t.Errorf("Estimate(%q) = %v, want Light", prompt, tier)
		}
	}
}

func TestEstimateHeavyKeywords(t *testing.T) {
	prompts := []string{
		"refactor the authentication module",
		"debug the memory leak",
		"design a new API for users",
		"architecture review of the codebase",
		"migrate from REST to gRPC",
		"rewrite the parser from scratch",
		"optimize the database queries",
		"implement new caching layer",
		"performance profiling of the server",
	}

	for _, prompt := range prompts {
		tier := Estimate(prompt)
		if tier != Heavy {
			t.Errorf("Estimate(%q) = %v, want Heavy", prompt, tier)
		}
	}
}

func TestEstimateDefaultMedium(t *testing.T) {
	prompts := []string{
		"add a new endpoint for user registration",
		"fix the login bug",
		"write unit tests for the handler",
		"update the documentation",
		"change the color of the button",
		"something completely unrelated",
		"",
		"   ",
	}

	for _, prompt := range prompts {
		tier := Estimate(prompt)
		if tier != Medium {
			t.Errorf("Estimate(%q) = %v, want Medium", prompt, tier)
		}
	}
}

func TestEstimateCaseInsensitive(t *testing.T) {
	prompts := []string{
		"LINT THE CODE",
		"Lint The Code",
		"REFACTOR the module",
		"ReFactor The Module",
	}

	expected := []Tier{Light, Light, Heavy, Heavy}
	for i, prompt := range prompts {
		tier := Estimate(prompt)
		if tier != expected[i] {
			t.Errorf("Estimate(%q) = %v, want %v", prompt, tier, expected[i])
		}
	}
}

func TestEstimateHeavyOverridesLight(t *testing.T) {
	// A prompt that contains both a Light and a Heavy keyword.
	// Heavy should win.
	prompt := "refactor the code and fix formatting"
	tier := Estimate(prompt)
	if tier != Heavy {
		t.Errorf("Estimate(%q) = %v, want Heavy (heavy overrides light)", prompt, tier)
	}
}

func TestEstimateWithHints(t *testing.T) {
	// Prompt alone would be Medium, but hint contains a Light keyword.
	prompt := "fix this"
	hint := "just a simple lint fix"

	tier := Estimate(prompt, hint)
	if tier != Light {
		t.Errorf("Estimate(%q, %q) = %v, want Light (hint contributes)", prompt, hint, tier)
	}
}

func TestEstimateHintHeavy(t *testing.T) {
	// Prompt is light, but hint is heavy -- heavy wins.
	prompt := "fix this formatting"
	hint := "this is part of a larger architecture redesign"

	tier := Estimate(prompt, hint)
	if tier != Heavy {
		t.Errorf("Estimate(%q, %q) = %v, want Heavy (hint heavy overrides prompt light)", prompt, hint, tier)
	}
}

func TestEstimateMultipleHints(t *testing.T) {
	prompt := "update the file"
	hint1 := "debug the issue"
	hint2 := "check the logs"

	tier := Estimate(prompt, hint1, hint2)
	if tier != Heavy {
		t.Errorf("Estimate with multiple hints should be Heavy, got %v", tier)
	}
}

func TestEstimatePartialMatch(t *testing.T) {
	// Substring matching: "reformat" contains "format".
	tier := Estimate("reformat the entire codebase")
	if tier != Light {
		t.Errorf("Estimate with partial match 'reformat' should be Light, got %v", tier)
	}
}

func TestEstimateOptimizationMatchesOptimize(t *testing.T) {
	// "optimization" contains "optimize".
	tier := Estimate("database optimization needed")
	if tier != Heavy {
		t.Errorf("Estimate with 'optimization' should be Heavy (contains 'optimize'), got %v", tier)
	}
}

func TestTierString(t *testing.T) {
	tests := []struct {
		tier Tier
		want string
	}{
		{Light, "light"},
		{Medium, "medium"},
		{Heavy, "heavy"},
		{Tier(99), "unknown"},
	}
	for _, tt := range tests {
		got := tt.tier.String()
		if got != tt.want {
			t.Errorf("Tier(%d).String() = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestLightKeywordsNonEmpty(t *testing.T) {
	if len(lightKeywords) == 0 {
		t.Error("lightKeywords list is empty")
	}
}

func TestHeavyKeywordsNonEmpty(t *testing.T) {
	if len(heavyKeywords) == 0 {
		t.Error("heavyKeywords list is empty")
	}
}

func TestEstimateLongPrompt(t *testing.T) {
	// Build a long prompt with a heavy keyword buried in the middle.
	parts := make([]string, 100)
	for i := range parts {
		parts[i] = "write some code that does something useful"
	}
	parts[50] = "debug the issue in the handler"
	longPrompt := strings.Join(parts, " ")

	tier := Estimate(longPrompt)
	if tier != Heavy {
		t.Errorf("Estimate long prompt should find 'debug' and be Heavy, got %v", tier)
	}
}

func TestEstimateWhitespaceOnly(t *testing.T) {
	tier := Estimate("   \t\n  ")
	if tier != Medium {
		t.Errorf("Estimate whitespace-only should be Medium, got %v", tier)
	}
}
