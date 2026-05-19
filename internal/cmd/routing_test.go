package cmd

import (
	"testing"

	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/router"
)

func TestSelectModelAutoLight(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		SonnetModel: "default-model",
		Routing: config.RoutingConfig{
			Light:  "light-model",
			Medium: "medium-model",
			Heavy:  "heavy-model",
		},
	}

	model := SelectModel(cfg, &Flags{Tier: ""}, "lint the code")
	if model != "light-model" {
		t.Errorf("SelectModel for 'lint' = %q, want light-model", model)
	}
}

func TestSelectModelAutoHeavy(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		SonnetModel: "default-model",
		Routing: config.RoutingConfig{
			Light:  "light-model",
			Medium: "medium-model",
			Heavy:  "heavy-model",
		},
	}

	model := SelectModel(cfg, &Flags{Tier: ""}, "refactor the module")
	if model != "heavy-model" {
		t.Errorf("SelectModel for 'refactor' = %q, want heavy-model", model)
	}
}

func TestSelectModelAutoMedium(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		SonnetModel: "default-model",
		Routing: config.RoutingConfig{
			Light:  "light-model",
			Medium: "medium-model",
			Heavy:  "heavy-model",
		},
	}

	model := SelectModel(cfg, &Flags{Tier: ""}, "fix the login bug")
	if model != "medium-model" {
		t.Errorf("SelectModel for generic prompt = %q, want medium-model", model)
	}
}

func TestSelectModelExplicitTier(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		SonnetModel: "default-model",
		Routing: config.RoutingConfig{
			Light:  "light-model",
			Medium: "medium-model",
			Heavy:  "heavy-model",
		},
	}

	model := SelectModel(cfg, &Flags{Tier: "heavy"}, "lint the code")
	if model != "heavy-model" {
		t.Errorf("SelectModel with --tier heavy should be heavy-model, got %q", model)
	}
}

func TestSelectModelNoRoutingConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		SonnetModel: "default-model",
		Routing:     config.RoutingConfig{}, // empty
	}

	model := SelectModel(cfg, &Flags{Tier: ""}, "lint the code")
	if model != "default-model" {
		t.Errorf("SelectModel without routing config = %q, want default-model", model)
	}
}

func TestSelectModelPartialRoutingConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		SonnetModel: "default-model",
		OpusModel:   "opus-model",
		Routing: config.RoutingConfig{
			Light: "light-model",
			// Medium and Heavy not configured
		},
	}

	// Light has a routing model.
	model := SelectModel(cfg, &Flags{Tier: ""}, "lint the code")
	if model != "light-model" {
		t.Errorf("SelectModel light = %q, want light-model", model)
	}

	// Medium falls back to SonnetModel.
	model = SelectModel(cfg, &Flags{Tier: ""}, "fix the bug")
	if model != "default-model" {
		t.Errorf("SelectModel medium fallback = %q, want default-model", model)
	}

	// Heavy falls back to OpusModel.
	model = SelectModel(cfg, &Flags{Tier: ""}, "refactor everything")
	if model != "opus-model" {
		t.Errorf("SelectModel heavy fallback = %q, want opus-model", model)
	}
}

func TestSelectModelFlagModelOverridesTier(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		SonnetModel: "default-model",
		Routing: config.RoutingConfig{
			Light: "light-model",
		},
	}

	// Explicit --model should take priority over routing.
	model := SelectModel(cfg, &Flags{Tier: "", Model: "custom-model"}, "lint the code")
	if model != "custom-model" {
		t.Errorf("SelectModel with --model should be custom-model, got %q", model)
	}
}

func TestSelectModelAutoTierUsesEstimator(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		SonnetModel: "default-model",
		Routing: config.RoutingConfig{
			Light:  "light-model",
			Medium: "medium-model",
			Heavy:  "heavy-model",
		},
	}

	// Verify that "auto" tier runs the estimator.
	model := SelectModel(cfg, &Flags{Tier: "auto"}, "format the code")
	if model != "light-model" {
		t.Errorf("SelectModel auto for 'format' = %q, want light-model", model)
	}
}

func TestTierToRouterTier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		flag string
		want router.Tier
		ok   bool
	}{
		{"light", router.Light, true},
		{"medium", router.Medium, true},
		{"heavy", router.Heavy, true},
		{"auto", router.Medium, false},    // auto is not a tier, it triggers estimation
		{"", router.Medium, false},        // empty triggers estimation
		{"invalid", router.Medium, false}, // invalid value
	}

	for _, tt := range tests {
		tier, ok := tierFromString(tt.flag)
		if ok != tt.ok {
			t.Errorf("tierFromString(%q) ok = %v, want %v", tt.flag, ok, tt.ok)
		}
		if ok && tier != tt.want {
			t.Errorf("tierFromString(%q) = %v, want %v", tt.flag, tier, tt.want)
		}
	}
}
