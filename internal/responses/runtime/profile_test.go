package runtime

import "testing"

func TestConfigContextBudgetForModelResolvesProfileAndFallback(t *testing.T) {
	cfg := Config{
		CompactHistoryItemThreshold: 20,
		ModelProfiles: []ModelProfile{
			{
				Name:          "exact-model",
				UpstreamModel: "upstream-exact",
				Budget: ContextBudget{
					ContextWindowTokens:         32768,
					MaxOutputTokens:             2048,
					CompactHistoryItemThreshold: 4,
				},
			},
			{
				Pattern: "gpt-4o*",
				Budget: ContextBudget{
					CompactHistoryItemThreshold: 8,
				},
			},
		},
	}

	exact := cfg.ContextBudgetForModel(" exact-model ")
	if exact.Profile == nil || exact.Profile.Name != "exact-model" || exact.Profile.UpstreamModel != "upstream-exact" {
		t.Fatalf("exact profile = %#v", exact.Profile)
	}
	if exact.Budget.ContextWindowTokens != 32768 || exact.Budget.MaxOutputTokens != 2048 || exact.Budget.CompactHistoryItemThreshold != 4 {
		t.Fatalf("exact budget = %+v", exact.Budget)
	}

	pattern := cfg.ContextBudgetForModel("gpt-4o-mini")
	if pattern.Profile == nil || pattern.Profile.Pattern != "gpt-4o*" {
		t.Fatalf("pattern profile = %#v", pattern.Profile)
	}
	if pattern.Budget.ContextWindowTokens != 0 || pattern.Budget.MaxOutputTokens != 0 || pattern.Budget.CompactHistoryItemThreshold != 8 {
		t.Fatalf("pattern budget = %+v", pattern.Budget)
	}

	fallback := cfg.ContextBudgetForModel("other-model")
	if fallback.Profile != nil {
		t.Fatalf("fallback profile = %#v, want nil", fallback.Profile)
	}
	if fallback.Budget.CompactHistoryItemThreshold != 20 {
		t.Fatalf("fallback threshold = %d, want 20", fallback.Budget.CompactHistoryItemThreshold)
	}
}

func TestConfigContextBudgetForModelPrefersExactBeforePattern(t *testing.T) {
	cfg := Config{
		ModelProfiles: []ModelProfile{
			{
				Pattern: "gpt-*",
				Budget: ContextBudget{
					ContextWindowTokens: 100,
				},
			},
			{
				Name: "gpt-5",
				Budget: ContextBudget{
					ContextWindowTokens: 200,
				},
			},
		},
	}

	resolved := cfg.ContextBudgetForModel("gpt-5")
	if resolved.Profile == nil || resolved.Profile.Name != "gpt-5" {
		t.Fatalf("resolved profile = %#v, want exact gpt-5", resolved.Profile)
	}
	if resolved.Budget.ContextWindowTokens != 200 {
		t.Fatalf("context window = %d, want exact profile value 200", resolved.Budget.ContextWindowTokens)
	}
}
