package main

import (
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
)

// promptDeliveryBudgetDoctorCheck verifies every agent's rendered prompt
// clears promptDelivery's size budget for its resolved provider/runtime
// (ga-q8wgom.1.1/.2), so a fleet-wide oversized-prompt hard-fail is caught
// by `gc doctor` instead of surfacing only at session start for whichever
// agent happens to prime first. It reuses promptDelivery directly rather
// than re-deriving thresholds, byte-accounting, or delivery-mode selection.
type promptDeliveryBudgetDoctorCheck struct {
	cityPath string
	cfg      *config.City
	lookPath config.LookPathFunc
}

func newPromptDeliveryBudgetDoctorCheck(cityPath string, cfg *config.City, lookPath config.LookPathFunc) *promptDeliveryBudgetDoctorCheck {
	return &promptDeliveryBudgetDoctorCheck{cityPath: cityPath, cfg: cfg, lookPath: lookPath}
}

// Name implements doctor.Check.
func (*promptDeliveryBudgetDoctorCheck) Name() string { return "prompt-delivery-budget" }

// CanFix implements doctor.Check. Fixing means editing prompt templates or
// fragments, which belongs to the user, not an automated fix.
func (*promptDeliveryBudgetDoctorCheck) CanFix() bool { return false }

// WarmupEligible implements doctor.Check. Rendering every agent's prompt
// depends on full city/rig configuration (pack dirs, fragments, rig
// resolution, provider resolution), so this check is ineligible for
// `gc start` warm-up, which runs before that configuration is available.
func (*promptDeliveryBudgetDoctorCheck) WarmupEligible() bool { return false }

// Fix implements doctor.Check.
func (*promptDeliveryBudgetDoctorCheck) Fix(_ *doctor.CheckContext) error { return nil }

// Run implements doctor.Check.
//
// TODO(ga-q8wgom.1.3 GREEN): this is a RED-stage stub. It must enumerate
// cfg.Agents (sorted by name), resolve each agent's provider, render its
// prompt (capturing render errors via a stderr buffer instead of letting
// renderPrompt swallow them), and call promptDelivery to classify each
// agent as OK / oversized-fallback / hard-fail — aggregating to the
// worst-case status (Error > Warning > OK) without leaking rendered
// prompt content into the result.
func (c *promptDeliveryBudgetDoctorCheck) Run(_ *doctor.CheckContext) *doctor.CheckResult {
	_ = c.cityPath
	_ = c.lookPath
	if c.cfg == nil {
		return okCheck("prompt-delivery-budget", "no city config")
	}
	return okCheck("prompt-delivery-budget", "not yet implemented")
}
