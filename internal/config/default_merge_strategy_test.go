package config

import (
	"strings"
	"testing"
)

// TestDefaultMergeStrategyRoundTrip pins the TOML key for the per-rig sling
// merge default: repos whose mainline requires PRs set
// default_merge_strategy = "mr" so beads slung without --merge land via
// MR instead of reaching the refinery as direct (gcy-br7).
func TestDefaultMergeStrategyRoundTrip(t *testing.T) {
	c := City{
		Workspace: Workspace{Name: "test"},
		Rigs: []Rig{
			{Name: "wow_logs", Path: "/tmp/wl", DefaultMergeStrategy: "mr"},
		},
	}
	data, err := c.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(Marshal output): %v", err)
	}
	if got.Rigs[0].DefaultMergeStrategy != "mr" {
		t.Errorf("DefaultMergeStrategy = %q, want %q", got.Rigs[0].DefaultMergeStrategy, "mr")
	}
}

// TestValidateRigs_DefaultMergeStrategy pins the accepted values to the same
// set gc sling --merge and the sling API accept (direct, mr, local). "pr" is
// rejected even though the refinery tolerates it: the refinery's pr alias
// exists for hand-written metadata, while everything sling writes goes
// through the CLI/API-validated set.
func TestValidateRigs_DefaultMergeStrategy(t *testing.T) {
	for _, strategy := range []string{"", "direct", "mr", "local"} {
		rigs := []Rig{{Name: "wow_logs", Path: "/tmp/wl", DefaultMergeStrategy: strategy}}
		if err := ValidateRigs(rigs, "mc"); err != nil {
			t.Errorf("ValidateRigs(default_merge_strategy=%q): unexpected error: %v", strategy, err)
		}
	}
	for _, strategy := range []string{"bogus", "MR", "pr", "direct-push"} {
		rigs := []Rig{{Name: "wow_logs", Path: "/tmp/wl", DefaultMergeStrategy: strategy}}
		err := ValidateRigs(rigs, "mc")
		if err == nil {
			t.Fatalf("ValidateRigs(default_merge_strategy=%q): expected error", strategy)
		}
		if !strings.Contains(err.Error(), "default_merge_strategy") {
			t.Errorf("error = %q, want mention of default_merge_strategy", err)
		}
	}
}

// TestApplyPatches_RigDefaultMergeStrategy pins the layered-config path:
// fragments and [[patches.rig]] entries can set the default.
func TestApplyPatches_RigDefaultMergeStrategy(t *testing.T) {
	cfg := &City{
		Rigs: []Rig{{Name: "wow_logs", Path: "/tmp/wl"}},
	}
	err := ApplyPatches(cfg, Patches{
		Rigs: []RigPatch{
			{Name: "wow_logs", DefaultMergeStrategy: ptrStr("mr")},
		},
	})
	if err != nil {
		t.Fatalf("ApplyPatches: %v", err)
	}
	if cfg.Rigs[0].DefaultMergeStrategy != "mr" {
		t.Errorf("DefaultMergeStrategy = %q, want %q", cfg.Rigs[0].DefaultMergeStrategy, "mr")
	}
}

// TestRigEffectiveDefaultMergeStrategy pins whitespace trimming so a
// padded config value still matches the validated enum at read sites.
func TestRigEffectiveDefaultMergeStrategy(t *testing.T) {
	r := Rig{DefaultMergeStrategy: "  mr  "}
	if got := r.EffectiveDefaultMergeStrategy(); got != "mr" {
		t.Errorf("EffectiveDefaultMergeStrategy = %q, want %q", got, "mr")
	}
}
