package cmd

import (
	"strings"

	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/router"
)

// SelectModel chooses the appropriate model based on routing config, tier flag,
// and prompt complexity estimation. The selection order is:
//
//  1. If --model flag is set, use it (explicit override, no routing).
//  2. If --tier is set (light/medium/heavy), use the model for that tier.
//  3. If --tier is "auto" or empty, run router.Estimate and use the tier model.
//  4. Fallback: routing.Light -> SonnetModel, routing.Medium -> SonnetModel,
//     routing.Heavy -> OpusModel.
func SelectModel(cfg *config.Config, flags *Flags, prompt string) string {
	// Explicit --model overrides everything.
	if flags.Model != "" {
		return flags.Model
	}

	// Determine tier.
	var tier router.Tier
	if t, ok := tierFromString(flags.Tier); ok {
		tier = t
	} else {
		// Auto: estimate from prompt.
		tier = router.Estimate(prompt)
	}

	// Map tier to model.
	switch tier {
	case router.Light:
		if cfg.Routing.Light != "" {
			return cfg.Routing.Light
		}
		return cfg.SonnetModel
	case router.Heavy:
		if cfg.Routing.Heavy != "" {
			return cfg.Routing.Heavy
		}
		return cfg.OpusModel
	default: // Medium
		if cfg.Routing.Medium != "" {
			return cfg.Routing.Medium
		}
		return cfg.SonnetModel
	}
}

// tierFromString converts a tier flag value to a router.Tier.
// Returns (tier, true) for valid explicit tiers (light, medium, heavy).
// Returns (Medium, false) for "auto", empty, or invalid values.
func tierFromString(s string) (router.Tier, bool) {
	switch strings.ToLower(s) {
	case "light":
		return router.Light, true
	case "medium":
		return router.Medium, true
	case "heavy":
		return router.Heavy, true
	default:
		return router.Medium, false
	}
}
