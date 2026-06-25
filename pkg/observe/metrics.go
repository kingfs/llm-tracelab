package observe

func observationTimings(input ParseInput) ObservationTimings {
	durationMs := input.Header.Meta.DurationMs
	ttftMs := input.Header.Meta.TTFTMs
	pp := tokenRate(input.Header.Usage.PromptTokens, ttftMs)
	tg := tokenRate(input.Header.Usage.CompletionTokens, durationMs-ttftMs)
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

func tokenRate(tokens int, durationMs int64) float64 {
	if tokens <= 0 || durationMs <= 0 {
		return 0
	}
	return float64(tokens) * 1000 / float64(durationMs)
}
