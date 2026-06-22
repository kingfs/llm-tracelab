package runtime

import "strings"

type ContextBudget struct {
	ContextWindowTokens         int
	MaxOutputTokens             int
	CompactHistoryItemThreshold int
}

type ModelProfile struct {
	Name          string
	Pattern       string
	UpstreamModel string
	Budget        ContextBudget
}

type ResolvedModelProfile struct {
	Profile *ModelProfile
	Budget  ContextBudget
}

func (c Config) ContextBudgetForModel(model string) ResolvedModelProfile {
	resolved := ResolvedModelProfile{
		Budget: ContextBudget{
			CompactHistoryItemThreshold: c.CompactHistoryItemThreshold,
		},
	}
	for i := range c.ModelProfiles {
		profile := c.ModelProfiles[i].normalized()
		if !profile.matches(model) {
			continue
		}
		resolved.Profile = &profile
		if profile.Budget.ContextWindowTokens > 0 {
			resolved.Budget.ContextWindowTokens = profile.Budget.ContextWindowTokens
		}
		if profile.Budget.MaxOutputTokens > 0 {
			resolved.Budget.MaxOutputTokens = profile.Budget.MaxOutputTokens
		}
		if profile.Budget.CompactHistoryItemThreshold > 0 {
			resolved.Budget.CompactHistoryItemThreshold = profile.Budget.CompactHistoryItemThreshold
		}
		return resolved
	}
	return resolved
}

func (p ModelProfile) normalized() ModelProfile {
	p.Name = strings.TrimSpace(p.Name)
	p.Pattern = strings.TrimSpace(p.Pattern)
	p.UpstreamModel = strings.TrimSpace(p.UpstreamModel)
	return p
}

func (p ModelProfile) matches(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	if p.Name != "" && p.Name == model {
		return true
	}
	if p.Pattern != "" && wildcardMatch(p.Pattern, model) {
		return true
	}
	return false
}

func wildcardMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	pi, vi := 0, 0
	star, match := -1, 0
	for vi < len(value) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == value[vi]) {
			pi++
			vi++
			continue
		}
		if pi < len(pattern) && pattern[pi] == '*' {
			star = pi
			match = vi
			pi++
			continue
		}
		if star != -1 {
			pi = star + 1
			match++
			vi = match
			continue
		}
		return false
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
