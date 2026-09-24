package nudgepoller

import "testing"

func TestCityCmdlineMatcher(t *testing.T) {
	match := CityCmdlineMatcher("/city/one")
	for _, tc := range []struct {
		name string
		argv []string
		want bool
	}{
		{name: "owned sidecar", argv: append([]string{"gc"}, CommandArgs("/city/one", "worker", "session-id")...), want: true},
		{name: "equals flag", argv: []string{"gc", "nudge", "poll", "--city=/city/one", "--session=worker", "session-id"}, want: true},
		{name: "other city", argv: append([]string{"gc"}, CommandArgs("/city/one-other", "worker", "session-id")...)},
		{name: "other command", argv: []string{"gc", "nudge", "drain", "--city", "/city/one"}},
		{name: "embedded command text", argv: []string{"sh", "-c", "gc", "nudge", "poll", "--city", "/city/one"}},
		{name: "ambiguous duplicate city", argv: []string{"gc", "nudge", "poll", "--city=/city/one", "--city=/city/two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := match(tc.argv); got != tc.want {
				t.Fatalf("match(%q) = %v, want %v", tc.argv, got, tc.want)
			}
		})
	}
}
