package observe

const minReliableGenerationWindowMs int64 = 10

func observationTimings(input ParseInput) ObservationTimings {
	durationMs := input.Header.Meta.DurationMs
	ttftMs := input.Header.Meta.TTFTMs
	pp := tokenRate(input.Header.Usage.PromptTokens, ttftMs)
	tg := generationTokenRate(input.Header.Usage.CompletionTokens, durationMs, ttftMs)
	return ObservationTimings{
		StartedAt:              input.Header.Meta.Time,
		DurationMs:             durationMs,
		TTFTMs:                 ttftMs,
		TokensPerSec:           tokenRate(input.Header.Usage.TotalTokens, durationMs),
		PPTokensPerSec:         pp,
		TGTokensPerSec:         tg,
		PrefillTokensPerSec:    pp,
		GenerationTokensPerSec: tg,
	}
}

func generationTokenRate(tokens int, durationMs int64, ttftMs int64) float64 {
	generationMs := durationMs - ttftMs
	if generationMs < minReliableGenerationWindowMs {
		return 0
	}
	return tokenRate(tokens, generationMs)
}

func tokenRate(tokens int, durationMs int64) float64 {
	if tokens <= 0 || durationMs <= 0 {
		return 0
	}
	return float64(tokens) * 1000 / float64(durationMs)
}
