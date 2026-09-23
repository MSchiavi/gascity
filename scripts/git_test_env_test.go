package scripts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakefileTestEnvIgnoresUserGitConfiguration(t *testing.T) {
	repoRoot := repoRoot(t)
	makefile, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[commit]\n\tgpgsign = true\n"), 0o644); err != nil {
		t.Fatalf("write poisoned global git config: %v", err)
	}

	testMakefile := filepath.Join(t.TempDir(), "Makefile")
	content := string(makefile) + `
.PHONY: print-test-env-git
print-test-env-git:
	@$(TEST_ENV) sh -c 'printf "global=%s\nnosystem=%s\ngitdir=%s\ngpgsign=%s\n" "$$GIT_CONFIG_GLOBAL" "$$GIT_CONFIG_NOSYSTEM" "$${GIT_DIR-unset}" "$$(git config --global --get commit.gpgsign 2>/dev/null || printf unset)"'
`
	if err := os.WriteFile(testMakefile, []byte(content), 0o644); err != nil {
		t.Fatalf("write test Makefile: %v", err)
	}

	cmd := makeCommand("--no-print-directory", "-f", testMakefile, "print-test-env-git")
	cmd.Dir = repoRoot
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"USER=" + os.Getenv("USER"),
		"SHELL=/bin/sh",
		"GIT_DIR=/poison/.git",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make print-test-env-git failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"nosystem=1",
		"gitdir=unset",
		"gpgsign=unset",
	} {
		if !strings.Contains(string(out), want+"\n") {
			t.Errorf("TEST_ENV output missing %q:\n%s", want, out)
		}
	}

	// The global config is a real, writable, seeded file outside the user's
	// HOME — not the user's own config and not an unwritable sentinel (a
	// /dev/null sentinel breaks ensure_beads_role's global write path).
	var globalPath string
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "global="); ok {
			globalPath = v
		}
	}
	if globalPath == "" {
		t.Fatalf("TEST_ENV output missing global= line:\n%s", out)
	}
	if strings.HasPrefix(globalPath, home+string(os.PathSeparator)) {
		t.Errorf("GIT_CONFIG_GLOBAL %q resolves under the poisoned HOME %q", globalPath, home)
	}
	info, err := os.Stat(globalPath)
	if err != nil {
		t.Fatalf("GIT_CONFIG_GLOBAL %q does not exist: %v", globalPath, err)
	}
	if info.Mode().Perm()&0o200 == 0 {
		t.Errorf("GIT_CONFIG_GLOBAL %q is not writable (mode %v)", globalPath, info.Mode())
	}
}

func TestShardTestEnvsIgnoreUserGitConfiguration(t *testing.T) {
	repoRoot := repoRoot(t)
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"test-local-parallel", []string{"scripts/test-local-parallel", "fast"}},
		{"test-go-test-shard", []string{"scripts/test-go-test-shard", "./internal/sessionlog", "1", "1"}},
		{"test-integration-shard", []string{"scripts/test-integration-shard", "review-formulas-recovery"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t.TempDir()
			binDir := filepath.Join(fixture, "bin")
			home := filepath.Join(fixture, "home")
			for _, dir := range []string{binDir, home} {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[commit]\n\tgpgsign = true\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			probeOut := filepath.Join(fixture, "probe.out")
			probePath := filepath.Join(binDir, "probe")
			probe := fmt.Sprintf(`#!/bin/sh
printf 'nosystem=%%s\n' "$GIT_CONFIG_NOSYSTEM" > %s
printf 'global=%%s\n' "$GIT_CONFIG_GLOBAL" >> %s
printf 'gitdir=%%s\n' "${GIT_DIR-unset}" >> %s
if git config --global --get commit.gpgsign >/dev/null 2>&1; then
  printf 'gpgsign=set\n' >> %s
else
  printf 'gpgsign=unset\n' >> %s
fi
`, shellQuote(probeOut), shellQuote(probeOut), shellQuote(probeOut), shellQuote(probeOut), shellQuote(probeOut))
			if err := os.WriteFile(probePath, []byte(probe), 0o755); err != nil {
				t.Fatal(err)
			}
			fakeGo := fmt.Sprintf(`#!/bin/sh
if [ "$1" = env ]; then exec %s "$@"; fi
for arg do
  if [ "$arg" = -list ]; then
    printf 'TestRetryManagedPooledWorkerRecoversClaimedAttemptAfterCrash\nTestGitConfigProbe\n'
    exit 0
  fi
done
exec %s
`, shellQuote(realGo), shellQuote(probePath))
			if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.name == "test-local-parallel" {
				fakeXargs := fmt.Sprintf(`#!/bin/sh
cat >/dev/null
while [ "$1" != bash ]; do shift; done
exec "$@" %s
`, shellQuote("probe::"+probePath))
				if err := os.WriteFile(filepath.Join(binDir, "xargs"), []byte(fakeXargs), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := testCommand("bash", append([]string{filepath.Join(repoRoot, tc.args[0])}, tc.args[1:]...)...)
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"HOME="+home,
				"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
				"GIT_CONFIG_NOSYSTEM=0",
				"GIT_DIR=/poison/.git",
				"GC_PUSH_GATE_NO_CAP=1",
				"LOCAL_TEST_JOBS=1",
				"GO_TEST_TIMEOUT=999h",
				"GO_TEST_WATCHDOG_GRACE=off",
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", tc.name, err, out)
			}
			data, err := os.ReadFile(probeOut)
			if err != nil {
				t.Fatalf("%s: read child environment: %v\n%s", tc.name, err, out)
			}
			for _, want := range []string{"nosystem=1\n", "gitdir=unset\n", "gpgsign=unset\n"} {
				if !strings.Contains(string(data), want) {
					t.Errorf("%s child environment missing %q: %s", tc.name, want, data)
				}
			}
			global := ""
			for _, line := range strings.Split(string(data), "\n") {
				if value, ok := strings.CutPrefix(line, "global="); ok {
					global = value
				}
			}
			if global == "" || global == filepath.Join(home, ".gitconfig") {
				t.Errorf("%s child global config = %q, want seeded config", tc.name, global)
			}
		})
	}
}
