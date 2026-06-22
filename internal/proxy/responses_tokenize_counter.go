package proxy

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	responsesruntime "github.com/kingfs/llm-tracelab/internal/responses/runtime"
	"github.com/kingfs/llm-tracelab/internal/router"
)

type responsesTokenizeCounterProfile struct {
	name          string
	pattern       string
	upstreamModel string
	upstreamID    string
	timeout       time.Duration
}

type responsesProviderTokenizeCounter struct {
	profiles []responsesTokenizeCounterProfile
	targets  []*router.Target
	client   *http.Client
}

var _ responsesruntime.ChatPromptTokenCounter = (*responsesProviderTokenizeCounter)(nil)

func responsesTokenizeEstimatorOption(cfg *config.Config, rtr *router.Router, client *http.Client) (responsesruntime.Option, error) {
	counter, err := newResponsesProviderTokenizeCounter(cfg, rtr, client)
	if err != nil {
		return nil, err
	}
	if counter == nil {
		return nil, nil
	}
	return responsesruntime.WithTokenEstimator(responsesruntime.NewAdapterBackedTokenEstimator(counter)), nil
}

func newResponsesProviderTokenizeCounter(cfg *config.Config, rtr *router.Router, client *http.Client) (*responsesProviderTokenizeCounter, error) {
	if cfg == nil || rtr == nil {
		return nil, nil
	}
	profiles := cfg.ResponsesModelProfiles()
	counterProfiles := make([]responsesTokenizeCounterProfile, 0, len(profiles))
	for _, profile := range profiles {
		if !profile.TokenizeCounter.Enabled {
			continue
		}
		if profile.Name == "" && profile.Pattern == "" {
			return nil, fmt.Errorf("responses tokenize_counter profile requires name or pattern")
		}
		counterProfiles = append(counterProfiles, responsesTokenizeCounterProfile{
			name:          profile.Name,
			pattern:       profile.Pattern,
			upstreamModel: profile.UpstreamModel,
			upstreamID:    profile.TokenizeCounter.UpstreamID,
			timeout:       profile.TokenizeCounter.Timeout,
		})
	}
	if len(counterProfiles) == 0 {
		return nil, nil
	}
	targets := rtr.Targets()
	if len(targets) == 0 {
		return nil, fmt.Errorf("responses tokenize_counter is enabled but no router targets are available")
	}
	return &responsesProviderTokenizeCounter{
		profiles: counterProfiles,
		targets:  targets,
		client:   client,
	}, nil
}

func (c *responsesProviderTokenizeCounter) CountChatPromptTokens(req responsesruntime.ChatPromptTokenCountRequest) (int, error) {
	if c == nil {
		return 0, fmt.Errorf("responses tokenize counter is nil")
	}
	profile, ok := c.matchProfile(req.Model)
	if !ok {
		return 0, fmt.Errorf("responses tokenize counter is not configured for model %q", req.Model)
	}
	target, ok := c.matchTarget(profile)
	if !ok {
		return 0, fmt.Errorf("responses tokenize counter target is not configured for model %q", req.Model)
	}
	headers := http.Header{}
	target.Upstream.ApplyAuthHeaders(headers)
	counter, err := responsesruntime.NewHTTPTokenizeChatPromptTokenCounter(responsesruntime.HTTPTokenizeChatPromptTokenCounterConfig{
		BaseURL: target.Upstream.BaseURL,
		Model:   firstNonEmpty(profile.upstreamModel, req.Model),
		Headers: headers,
		Timeout: profile.timeout,
		Client:  c.client,
	})
	if err != nil {
		return 0, err
	}
	return counter.CountChatPromptTokens(req)
}

func (c *responsesProviderTokenizeCounter) matchProfile(model string) (responsesTokenizeCounterProfile, bool) {
	for _, profile := range c.profiles {
		if profileMatchesModel(profile.name, profile.pattern, model) {
			return profile, true
		}
	}
	return responsesTokenizeCounterProfile{}, false
}

func (c *responsesProviderTokenizeCounter) matchTarget(profile responsesTokenizeCounterProfile) (*router.Target, bool) {
	for _, target := range c.targets {
		if target == nil || !target.Enabled {
			continue
		}
		if profile.upstreamID != "" && target.ID != profile.upstreamID && target.RouteTargetID != profile.upstreamID && target.ChannelID != profile.upstreamID {
			continue
		}
		if target.Upstream.Capabilities.Tokenize == nil || !*target.Upstream.Capabilities.Tokenize {
			continue
		}
		return target, true
	}
	return nil, false
}

func profileMatchesModel(name string, pattern string, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	if name != "" && name == model {
		return true
	}
	return wildcardMatchString(pattern, model)
}

func wildcardMatchString(pattern string, value string) bool {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
