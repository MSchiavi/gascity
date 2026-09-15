package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
)

// clearPromptDeliveryBudgetEnv unsets the ambient GC_* variables that
// buildPrimeContextFor reads directly (GC_ALIAS, GC_AGENT, GC_DIR, GC_RIG,
// GC_RIG_ROOT), restoring any prior value on test cleanup. Without this, a
// test run from inside a real gc-managed session (which sets these) would
// leak ambient rig/agent identity into what should be a hermetic fixture.
func clearPromptDeliveryBudgetEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GC_ALIAS", "GC_AGENT", "GC_DIR", "GC_RIG", "GC_RIG_ROOT"} {
		old, had := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("clearPromptDeliveryBudgetEnv: unset %s: %v", k, err)
		}
		if had {
			t.Cleanup(func() { os.Setenv(k, old) }) //nolint:errcheck
		}
	}
}

// fakePromptDeliveryLookPath always fails. Every fixture in this file
// resolves its provider via the StartCommand escape hatch, which never
// calls lookPath, so a real PATH lookup is never exercised here.
func fakePromptDeliveryLookPath(string) (string, error) {
	return "", fmt.Errorf("fakePromptDeliveryLookPath: binary lookup is not available in tests")
}

// writePromptFile writes a prompt fixture at cityPath/relPath (creating
// parent directories as needed) and returns relPath, ready to assign to
// config.Agent.PromptTemplate (which is resolved relative to the city dir).
func writePromptFile(t *testing.T, cityPath, relPath, content string) string {
	t.Helper()
	full := filepath.Join(cityPath, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("writePromptFile: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writePromptFile: write: %v", err)
	}
	return relPath
}

// promptFixtureAgent builds a config.Agent that resolves its provider via
// the StartCommand escape hatch (so cfg.Providers/lookPath never come into
// play), with an explicit promptMode ("arg" unless a test needs "none") and
// session (the runtime-name input to promptDeliverySupportFor).
func promptFixtureAgent(name, promptTemplate, session, promptMode string) config.Agent {
	return config.Agent{
		Name:           name,
		StartCommand:   "true",
		PromptMode:     promptMode,
		Session:        session,
		PromptTemplate: promptTemplate,
	}
}

func runPromptDeliveryBudgetCheck(t *testing.T, cfg *config.City, cityPath string) *doctor.CheckResult {
	t.Helper()
	check := newPromptDeliveryBudgetDoctorCheck(cityPath, cfg, fakePromptDeliveryLookPath)
	return check.Run(&doctor.CheckContext{CityPath: cityPath})
}

func joinedDetails(res *doctor.CheckResult) string {
	return strings.Join(res.Details, "\n")
}

func TestPromptDeliveryBudgetCheck_NilConfig(t *testing.T) {
	check := newPromptDeliveryBudgetDoctorCheck("", nil, fakePromptDeliveryLookPath)
	res := check.Run(&doctor.CheckContext{})
	if res.Status != doctor.StatusOK {
		t.Fatalf("nil config: status = %v, want StatusOK; message=%q", res.Status, res.Message)
	}
}

func TestPromptDeliveryBudgetCheck_NoAgents(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	cfg := &config.City{Workspace: config.Workspace{Name: "demo"}}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusOK {
		t.Fatalf("no agents: status = %v, want StatusOK; message=%q details=%v", res.Status, res.Message, res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_SafePrompt_RawThreshold(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	body := strings.Repeat("a", 99999) // raw=99999 (<100000), quoted=100001 (<128000): safe
	tmpl := writePromptFile(t, cityPath, "prompts/safe-raw.md", body)
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{promptFixtureAgent("safe-raw", tmpl, "subprocess", "arg")},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusOK {
		t.Fatalf("safe raw-threshold prompt: status = %v, want StatusOK; message=%q details=%v", res.Status, res.Message, res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_SafePrompt_QuotedThreshold(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	body := strings.Repeat("'", 31999) // raw=31999, quoted=4*31999+2=127998 (<128000): safe
	tmpl := writePromptFile(t, cityPath, "prompts/safe-quoted.md", body)
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{promptFixtureAgent("safe-quoted", tmpl, "subprocess", "arg")},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusOK {
		t.Fatalf("safe quoted-threshold prompt: status = %v, want StatusOK; message=%q details=%v", res.Status, res.Message, res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_OversizedRaw_UnsupportedRuntime(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	body := strings.Repeat("a", 100000) // raw=100000 (>=100000): trips the raw guard
	tmpl := writePromptFile(t, cityPath, "prompts/oversized-raw.md", body)
	agent := promptFixtureAgent("oversized-raw-unsupported", tmpl, "subprocess", "arg")
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{agent},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusError {
		t.Fatalf("oversized raw on unsupported runtime: status = %v, want StatusError; message=%q details=%v", res.Status, res.Message, res.Details)
	}
	details := joinedDetails(res)
	if !strings.Contains(details, agent.Name) {
		t.Errorf("details missing agent name %q: %v", agent.Name, res.Details)
	}
	if !strings.Contains(details, "subprocess") {
		t.Errorf("details missing runtime name %q: %v", "subprocess", res.Details)
	}
	if !strings.Contains(details, "hard-fail") {
		t.Errorf("details missing hard-fail classification: %v", res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_OversizedQuoted_UnsupportedRuntime(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	body := strings.Repeat("'", 32000) // raw=32000 (safe), quoted=4*32000+2=128002 (>=128000): trips the quoted guard
	tmpl := writePromptFile(t, cityPath, "prompts/oversized-quoted.md", body)
	agent := promptFixtureAgent("oversized-quoted-unsupported", tmpl, "subprocess", "arg")
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{agent},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusError {
		t.Fatalf("oversized quoted on unsupported runtime: status = %v, want StatusError; message=%q details=%v", res.Status, res.Message, res.Details)
	}
	details := joinedDetails(res)
	if !strings.Contains(details, agent.Name) {
		t.Errorf("details missing agent name %q: %v", agent.Name, res.Details)
	}
	if !strings.Contains(details, "hard-fail") {
		t.Errorf("details missing hard-fail classification: %v", res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_OversizedRaw_NudgeFallbackRuntime(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	body := strings.Repeat("a", 100000)
	tmpl := writePromptFile(t, cityPath, "prompts/oversized-raw-tmux.md", body)
	agent := promptFixtureAgent("oversized-raw-tmux", tmpl, "tmux", "arg")
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{agent},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusWarning {
		t.Fatalf("oversized raw on nudge-fallback runtime: status = %v, want StatusWarning; message=%q details=%v", res.Status, res.Message, res.Details)
	}
	details := joinedDetails(res)
	if !strings.Contains(details, agent.Name) {
		t.Errorf("details missing agent name %q: %v", agent.Name, res.Details)
	}
	if !strings.Contains(details, "tmux") {
		t.Errorf("details missing runtime name %q: %v", "tmux", res.Details)
	}
	if !strings.Contains(details, "nudge") {
		t.Errorf("details missing nudge-fallback classification: %v", res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_OversizedQuoted_NudgeFallbackRuntime(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	body := strings.Repeat("'", 32000)
	tmpl := writePromptFile(t, cityPath, "prompts/oversized-quoted-default.md", body)
	// Session left empty; cfg.Session.Provider is also empty (zero value), so
	// effectiveSessionProvider resolves to "" — one of the built-in
	// nudge-fallback runtime names alongside "tmux"/"herdr"/"k8s"/"hybrid".
	agent := promptFixtureAgent("oversized-quoted-default", tmpl, "", "arg")
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{agent},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusWarning {
		t.Fatalf("oversized quoted on default (empty) runtime: status = %v, want StatusWarning; message=%q details=%v", res.Status, res.Message, res.Details)
	}
	details := joinedDetails(res)
	if !strings.Contains(details, agent.Name) {
		t.Errorf("details missing agent name %q: %v", agent.Name, res.Details)
	}
	if !strings.Contains(details, "nudge") {
		t.Errorf("details missing nudge-fallback classification: %v", res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_ACPSession_BypassesSizeGuard(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	body := strings.Repeat("a", 100000) // oversized by raw count, but isACP short-circuits before the guard
	tmpl := writePromptFile(t, cityPath, "prompts/acp.md", body)
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{promptFixtureAgent("acp-agent", tmpl, "acp", "arg")},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusOK {
		t.Fatalf("ACP session with oversized body: status = %v, want StatusOK (ACP bypasses the size guard entirely); message=%q details=%v", res.Status, res.Message, res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_PromptModeNone_BypassesSizeGuard(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	body := strings.Repeat("a", 100000) // oversized by raw count, but PromptMode "none" short-circuits before the guard
	tmpl := writePromptFile(t, cityPath, "prompts/promptmode-none.md", body)
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{promptFixtureAgent("none-mode-agent", tmpl, "subprocess", "none")},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusOK {
		t.Fatalf("prompt_mode=none with oversized body: status = %v, want StatusOK (\"none\" bypasses the size guard entirely); message=%q details=%v", res.Status, res.Message, res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_UnresolvableProvider_DoesNotBlockDeliveryCheck(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	tmpl := writePromptFile(t, cityPath, "prompts/badprovider.md", "short prompt body")
	agent := config.Agent{
		Name:           "badprovider-agent",
		Provider:       "does-not-exist",
		PromptMode:     "arg",
		PromptTemplate: tmpl,
		// No StartCommand: forces provider-name resolution, which fails
		// because "does-not-exist" is absent from cfg.Providers (nil/empty).
		// ResolveProvider's error is intentionally ignored here (mirroring
		// cmd_prime.go), so this agent's prompt is still judged purely on
		// delivery-budget merits: a short, well-formed prompt should clear
		// with StatusOK rather than being hard-failed for an unrelated,
		// out-of-scope provider misconfiguration (that's provider-catalog's
		// job, not this check's).
	}
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{agent},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusOK {
		t.Fatalf("unresolvable provider with a safe prompt: status = %v, want StatusOK; message=%q details=%v", res.Status, res.Message, res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_RenderError(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	// .template.md forces actual Go-template execution (unlike a plain .md
	// passthrough). buildTemplateData flattens PromptContext into a
	// map[string]string, and the "missingkey=zero" option silently zeroes an
	// absent top-level key rather than erroring — so a bare {{.NoSuchField}}
	// would NOT fail. Chaining a further field access onto that zeroed
	// string result does fail (a string has no fields), so tmpl.Execute
	// errors and renderPromptWithMeta falls back to the raw unrendered body
	// (tiny, nowhere near either size threshold).
	tmpl := writePromptFile(t, cityPath, "prompts/broken.template.md", "{{.NoSuchField.Nested}}")
	agent := promptFixtureAgent("render-error-agent", tmpl, "subprocess", "arg")
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{agent},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusWarning {
		t.Fatalf("template render error: status = %v, want StatusWarning; message=%q details=%v", res.Status, res.Message, res.Details)
	}
	details := joinedDetails(res)
	if !strings.Contains(details, agent.Name) {
		t.Errorf("details missing agent name %q: %v", agent.Name, res.Details)
	}
	if !strings.Contains(details, "render") {
		t.Errorf("details missing render-warning wording: %v", res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_MultipleAgents_WorstCaseWins(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	okTmpl := writePromptFile(t, cityPath, "prompts/multi-ok.md", "short and safe")
	hardFailTmpl := writePromptFile(t, cityPath, "prompts/multi-hardfail.md", strings.Repeat("a", 100000))

	okAgent := promptFixtureAgent("multi-ok-agent", okTmpl, "subprocess", "arg")
	hardFailAgent := promptFixtureAgent("multi-hardfail-agent", hardFailTmpl, "subprocess", "arg")
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{okAgent, hardFailAgent},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusError {
		t.Fatalf("mixed OK+hard-fail agents: status = %v, want StatusError (worst case wins); message=%q details=%v", res.Status, res.Message, res.Details)
	}
	details := joinedDetails(res)
	if !strings.Contains(details, hardFailAgent.Name) {
		t.Errorf("details missing the hard-fail agent's name %q: %v", hardFailAgent.Name, res.Details)
	}
	if strings.Contains(details, okAgent.Name) {
		t.Errorf("details unexpectedly mention the OK agent %q, which contributed nothing: %v", okAgent.Name, res.Details)
	}
}

func TestPromptDeliveryBudgetCheck_SuspendedAgentSkipped(t *testing.T) {
	clearPromptDeliveryBudgetEnv(t)
	cityPath := t.TempDir()
	tmpl := writePromptFile(t, cityPath, "prompts/suspended-hardfail.md", strings.Repeat("a", 100000))
	agent := promptFixtureAgent("suspended-agent", tmpl, "subprocess", "arg")
	agent.Suspended = true
	cfg := &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Agents:    []config.Agent{agent},
	}

	res := runPromptDeliveryBudgetCheck(t, cfg, cityPath)
	if res.Status != doctor.StatusOK {
		t.Fatalf("suspended agent with an otherwise hard-fail prompt: status = %v, want StatusOK (suspended agents are skipped entirely); message=%q details=%v", res.Status, res.Message, res.Details)
	}
}

// The wiring is the fix: promptDeliveryBudgetDoctorCheck only runs at all if
// buildDoctorChecks registers it, mirroring
// TestBuildDoctorChecksRegistersRigWorktreesCheck's pattern for a different
// check.
func TestPromptDeliveryBudgetCheck_RegisteredInDoctorChecks(t *testing.T) {
	cityDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityDir, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.City{Workspace: config.Workspace{Name: "demo"}}
	checks := buildDoctorChecks(cityDir, cfg, nil, buildDoctorChecksOpts{
		ControllerRunning:    true,
		SkipCityDoltCheck:    true,
		SkipManagedDoltCheck: true,
	})

	names := doctorCheckNames(checks)
	if doctorCheckIndex(names, "prompt-delivery-budget") < 0 {
		t.Errorf("prompt-delivery-budget not registered; names=%v", names)
	}
}
