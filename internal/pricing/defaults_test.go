package pricing

import (
	"testing"
	"time"
)

func TestDefaultPricingsAllValid(t *testing.T) {
	for _, p := range DefaultPricings() {
		if err := p.Validate(); err != nil {
			t.Errorf("default %s/%s invalid: %v", p.Provider, p.Model, err)
		}
		if p.Tier.IsZero() {
			t.Errorf("default %s/%s has zero rates", p.Provider, p.Model)
		}
		if p.LastVerified == "" {
			t.Errorf("default %s/%s missing last_verified", p.Provider, p.Model)
		}
	}
}

func TestDefaultPricingsCoverKnownClaudeModels(t *testing.T) {
	known := []string{
		"claude-3-opus-20240229",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"claude-opus-4",
		"claude-sonnet-4-6",
		"claude-opus-4-7",
		"claude-opus-4-8",
		"claude-haiku-4-5-20251001",
	}
	r := New(DefaultPricings())
	for _, m := range known {
		if _, ok := r.Lookup("claude", m); !ok {
			t.Errorf("default Claude pricing missing for model %q", m)
		}
	}
}

func TestDefaultPricingsCurrentClaudeRates(t *testing.T) {
	tests := []struct {
		model         string
		prompt        float64
		completion    float64
		cacheRead     float64
		cacheCreation float64
	}{
		{
			model:         "claude-opus-4",
			prompt:        15.00,
			completion:    75.00,
			cacheRead:     1.50,
			cacheCreation: 18.75,
		},
		{
			model:         "claude-sonnet-4-6",
			prompt:        3.00,
			completion:    15.00,
			cacheRead:     0.30,
			cacheCreation: 3.75,
		},
		{
			model:         "claude-opus-4-7",
			prompt:        5.00,
			completion:    25.00,
			cacheRead:     0.50,
			cacheCreation: 6.25,
		},
		{
			model:         "claude-opus-4-8",
			prompt:        5.00,
			completion:    25.00,
			cacheRead:     0.50,
			cacheCreation: 6.25,
		},
		{
			model:         "claude-haiku-4-5-20251001",
			prompt:        1.00,
			completion:    5.00,
			cacheRead:     0.10,
			cacheCreation: 1.25,
		},
	}
	r := New(DefaultPricings())
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			got, ok := r.Lookup("claude", tc.model)
			if !ok {
				t.Fatalf("missing default pricing for %q", tc.model)
			}
			if got.Tier.PromptUSDPer1M != tc.prompt {
				t.Errorf("PromptUSDPer1M = %v, want %v", got.Tier.PromptUSDPer1M, tc.prompt)
			}
			if got.Tier.CompletionUSDPer1M != tc.completion {
				t.Errorf("CompletionUSDPer1M = %v, want %v", got.Tier.CompletionUSDPer1M, tc.completion)
			}
			if got.Tier.CacheReadUSDPer1M != tc.cacheRead {
				t.Errorf("CacheReadUSDPer1M = %v, want %v", got.Tier.CacheReadUSDPer1M, tc.cacheRead)
			}
			if got.Tier.CacheCreationUSDPer1M != tc.cacheCreation {
				t.Errorf("CacheCreationUSDPer1M = %v, want %v", got.Tier.CacheCreationUSDPer1M, tc.cacheCreation)
			}
		})
	}
}

func TestDefaultPricingsCoverKnownMuseModels(t *testing.T) {
	r := New(DefaultPricings())
	for _, m := range []string{
		"muse-spark-1.2",
		"muse-spark-1.2-contributor",
		"muse-spark-1.3",
		"muse-spark-1.3-contributor",
	} {
		if _, ok := r.Lookup("muse", m); !ok {
			t.Errorf("default Muse pricing missing for model %q", m)
		}
	}
}

func TestDefaultPricingsMuseRates(t *testing.T) {
	tests := []struct {
		model         string
		prompt        float64
		completion    float64
		cacheRead     float64
		cacheCreation float64
	}{
		{
			model:         "muse-spark-1.3",
			prompt:        1.25,
			completion:    4.25,
			cacheRead:     0.15,
			cacheCreation: 1.25,
		},
		{
			model:         "muse-spark-1.3-contributor",
			prompt:        0.10,
			completion:    0.20,
			cacheRead:     0.002,
			cacheCreation: 0.10,
		},
	}
	r := New(DefaultPricings())
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			got, ok := r.Lookup("muse", tc.model)
			if !ok {
				t.Fatalf("missing default pricing for %q", tc.model)
			}
			if got.Tier.PromptUSDPer1M != tc.prompt {
				t.Errorf("PromptUSDPer1M = %v, want %v", got.Tier.PromptUSDPer1M, tc.prompt)
			}
			if got.Tier.CompletionUSDPer1M != tc.completion {
				t.Errorf("CompletionUSDPer1M = %v, want %v", got.Tier.CompletionUSDPer1M, tc.completion)
			}
			if got.Tier.CacheReadUSDPer1M != tc.cacheRead {
				t.Errorf("CacheReadUSDPer1M = %v, want %v", got.Tier.CacheReadUSDPer1M, tc.cacheRead)
			}
			if got.Tier.CacheCreationUSDPer1M != tc.cacheCreation {
				t.Errorf("CacheCreationUSDPer1M = %v, want %v", got.Tier.CacheCreationUSDPer1M, tc.cacheCreation)
			}
		})
	}
}

func TestDefaultPricingsMuseEstimate(t *testing.T) {
	r := New(DefaultPricings())
	// One contributor-tier invocation: 160 fresh prompt tokens, 117
	// completion, 43889 cached reads — the shape the muse extractor emits.
	cost, ok := r.Estimate("muse", "muse-spark-1.3-contributor", Usage{
		PromptTokens:     160,
		CompletionTokens: 117,
		CacheReadTokens:  43889,
	})
	if !ok {
		t.Fatal("Estimate returned not-ok for muse-spark-1.3-contributor")
	}
	want := 160*0.10/1_000_000 + 117*0.20/1_000_000 + 43889*0.002/1_000_000
	if cost != want {
		t.Errorf("Estimate = %v, want %v", cost, want)
	}
}

// TestDefaultPricingsCacheReadIsCheaperThanPrompt protects against
// regressions that would conflate prompt and cache-read tiers — the
// motivating concern in #1255.
func TestDefaultPricingsCacheReadIsCheaperThanPrompt(t *testing.T) {
	for _, p := range DefaultPricings() {
		if p.Provider != "claude" {
			continue
		}
		if p.Tier.PromptUSDPer1M == 0 || p.Tier.CacheReadUSDPer1M == 0 {
			continue
		}
		ratio := p.Tier.PromptUSDPer1M / p.Tier.CacheReadUSDPer1M
		// Anthropic's cache-read is published at ~10% of the prompt rate.
		// Allow a generous band so future re-pricing doesn't fail this test.
		if ratio < 5 || ratio > 20 {
			t.Errorf("default %s/%s prompt/cache-read ratio = %.2f, expected ~10",
				p.Provider, p.Model, ratio)
		}
	}
}

func TestDefaultPricingsReturnsCopy(t *testing.T) {
	a := DefaultPricings()
	b := DefaultPricings()
	if &a[0] == &b[0] {
		t.Fatal("DefaultPricings() must return a fresh slice each call")
	}
	a[0].Tier.PromptUSDPer1M = 999
	for _, p := range b {
		if p.Tier.PromptUSDPer1M == 999 {
			t.Fatal("mutating one returned slice must not affect another")
		}
	}
}

func TestDefaultPricingsLastVerifiedParseable(t *testing.T) {
	for _, p := range DefaultPricings() {
		if _, err := time.Parse(LastVerifiedLayout, p.LastVerified); err != nil {
			t.Errorf("%s/%s last_verified=%q failed to parse: %v",
				p.Provider, p.Model, p.LastVerified, err)
		}
	}
}
