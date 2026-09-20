package pricing

// DefaultPricings returns the package-shipped default pricing entries.
//
// These are best-effort published rates as of LastVerified; they are
// decision-support only and operators are expected to override stale or
// inaccurate entries via [[pricing]] in city.toml or pack.toml.
//
// The non-goal stated in #1255 ("a hard-coded Go pricing table") refers to
// the model where rates can only be updated by shipping a new release.
// These defaults exist as a bootstrap so cost estimates work out of the box;
// users override via config without waiting on a release.
//
// Returned slice is freshly allocated; callers may mutate.
func DefaultPricings() []ModelPricing {
	out := make([]ModelPricing, 0, len(claudeDefaults)+len(museDefaults))
	out = append(out, claudeDefaults...)
	out = append(out, museDefaults...)
	return out
}

// claudeDefaults captures Anthropic's published Claude API rates.
//
// LastVerified is set conservatively; consumers should warn when entries
// exceed a configured staleness threshold. Cache-creation rates use the
// 5-minute (1.25× prompt) tier since that's the controller-default cache
// behavior in agent loops.
//
// See: https://www.anthropic.com/pricing
var claudeDefaults = []ModelPricing{
	// Claude 3 Opus (legacy).
	{
		Provider:     "claude",
		Model:        "claude-3-opus-20240229",
		LastVerified: "2026-04-25",
		Tier: Tier{
			PromptUSDPer1M:        15.00,
			CompletionUSDPer1M:    75.00,
			CacheReadUSDPer1M:     1.50,
			CacheCreationUSDPer1M: 18.75,
		},
	},
	// Claude 3.5 Sonnet (legacy, still common).
	{
		Provider:     "claude",
		Model:        "claude-3-5-sonnet-20241022",
		LastVerified: "2026-04-25",
		Tier: Tier{
			PromptUSDPer1M:        3.00,
			CompletionUSDPer1M:    15.00,
			CacheReadUSDPer1M:     0.30,
			CacheCreationUSDPer1M: 3.75,
		},
	},
	// Claude 3.5 Haiku.
	{
		Provider:     "claude",
		Model:        "claude-3-5-haiku-20241022",
		LastVerified: "2026-04-25",
		Tier: Tier{
			PromptUSDPer1M:        0.80,
			CompletionUSDPer1M:    4.00,
			CacheReadUSDPer1M:     0.08,
			CacheCreationUSDPer1M: 1.00,
		},
	},
	// Claude 4 Opus.
	{
		Provider:     "claude",
		Model:        "claude-opus-4",
		LastVerified: "2026-04-25",
		Tier: Tier{
			PromptUSDPer1M:        15.00,
			CompletionUSDPer1M:    75.00,
			CacheReadUSDPer1M:     1.50,
			CacheCreationUSDPer1M: 18.75,
		},
	},
	// Claude 4.6 Sonnet.
	{
		Provider:     "claude",
		Model:        "claude-sonnet-4-6",
		LastVerified: "2026-04-25",
		Tier: Tier{
			PromptUSDPer1M:        3.00,
			CompletionUSDPer1M:    15.00,
			CacheReadUSDPer1M:     0.30,
			CacheCreationUSDPer1M: 3.75,
		},
	},
	// Claude 4.7 Opus.
	{
		Provider:     "claude",
		Model:        "claude-opus-4-7",
		LastVerified: "2026-05-09",
		Tier: Tier{
			PromptUSDPer1M:        5.00,
			CompletionUSDPer1M:    25.00,
			CacheReadUSDPer1M:     0.50,
			CacheCreationUSDPer1M: 6.25,
		},
	},
	// Claude 4.8 Opus. Regular usage pricing is unchanged from Opus 4.7.
	{
		Provider:     "claude",
		Model:        "claude-opus-4-8",
		LastVerified: "2026-05-28",
		Tier: Tier{
			PromptUSDPer1M:        5.00,
			CompletionUSDPer1M:    25.00,
			CacheReadUSDPer1M:     0.50,
			CacheCreationUSDPer1M: 6.25,
		},
	},
	// Claude 4.5 Haiku.
	{
		Provider:     "claude",
		Model:        "claude-haiku-4-5-20251001",
		LastVerified: "2026-05-09",
		Tier: Tier{
			PromptUSDPer1M:        1.00,
			CompletionUSDPer1M:    5.00,
			CacheReadUSDPer1M:     0.10,
			CacheCreationUSDPer1M: 1.25,
		},
	},
}

// museDefaults captures Meta's published Muse Spark API rates.
//
// Standard tier ($1.25 input / $4.25 output / $0.15 cached input per 1M) is
// unchanged across Spark 1.1→1.3; the contributor tier (data-sharing opt-in)
// cuts those to $0.10 / $0.20 / $0.002. Meta publishes no cache-creation
// premium, so cache creation is rated at the prompt rate — an assumption,
// not a published figure; operators who learn otherwise override via
// [[pricing]] in city.toml or pack.toml.
//
// See: https://www.techtimes.com/articles/326714/20260904/meta-muse-spark-contributor-tier-hides-training-consent-where-security-tools-cannot-find-it.htm
// See: https://thearabianpost.com/meta-deploys-muse-spark-1-3-for-developers/
var museDefaults = []ModelPricing{
	// Muse Spark 1.3, standard tier.
	{
		Provider:     "muse",
		Model:        "muse-spark-1.3",
		LastVerified: "2026-09-20",
		Tier: Tier{
			PromptUSDPer1M:        1.25,
			CompletionUSDPer1M:    4.25,
			CacheReadUSDPer1M:     0.15,
			CacheCreationUSDPer1M: 1.25,
		},
	},
	// Muse Spark 1.3, contributor tier.
	{
		Provider:     "muse",
		Model:        "muse-spark-1.3-contributor",
		LastVerified: "2026-09-20",
		Tier: Tier{
			PromptUSDPer1M:        0.10,
			CompletionUSDPer1M:    0.20,
			CacheReadUSDPer1M:     0.002,
			CacheCreationUSDPer1M: 0.10,
		},
	},
	// Muse Spark 1.2, standard tier.
	{
		Provider:     "muse",
		Model:        "muse-spark-1.2",
		LastVerified: "2026-09-20",
		Tier: Tier{
			PromptUSDPer1M:        1.25,
			CompletionUSDPer1M:    4.25,
			CacheReadUSDPer1M:     0.15,
			CacheCreationUSDPer1M: 1.25,
		},
	},
	// Muse Spark 1.2, contributor tier.
	{
		Provider:     "muse",
		Model:        "muse-spark-1.2-contributor",
		LastVerified: "2026-09-20",
		Tier: Tier{
			PromptUSDPer1M:        0.10,
			CompletionUSDPer1M:    0.20,
			CacheReadUSDPer1M:     0.002,
			CacheCreationUSDPer1M: 0.10,
		},
	},
}
