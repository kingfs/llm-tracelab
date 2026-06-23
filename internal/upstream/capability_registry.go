package upstream

import (
	"net/http"
	"sort"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/config"
)

const (
	ProbeAuthOpenAI    = "openai"
	ProbeAuthAnthropic = "anthropic"
	ProbeAuthGemini    = "gemini"
)

type CapabilitySpec struct {
	Name      string
	FieldName string
}

type ProviderProbeSpec struct {
	ID             string
	Method         string
	Path           string
	ProtocolFamily string
	Capability     string
	Weight         float64
	Auth           string
	Body           string
}

var capabilityRegistry = []CapabilitySpec{
	{Name: CapabilityResponses, FieldName: "capabilities.responses"},
	{Name: CapabilityChatCompletions, FieldName: "capabilities.chat_completions"},
	{Name: CapabilityToolCalling, FieldName: "capabilities.tool_calling"},
	{Name: CapabilityEmbeddings, FieldName: "capabilities.embeddings"},
	{Name: CapabilityModels, FieldName: "capabilities.models"},
	{Name: CapabilityTokenize, FieldName: "capabilities.tokenize"},
}

var defaultProviderProbeSpecs = []ProviderProbeSpec{
	{
		ID:             "openai_models",
		Method:         http.MethodGet,
		Path:           "/v1/models",
		ProtocolFamily: ProtocolFamilyOpenAICompatible,
		Capability:     CapabilityModels,
		Weight:         1,
		Auth:           ProbeAuthOpenAI,
	},
	{
		ID:             "openai_chat_completions",
		Method:         http.MethodPost,
		Path:           "/v1/chat/completions",
		ProtocolFamily: ProtocolFamilyOpenAICompatible,
		Capability:     CapabilityChatCompletions,
		Weight:         3,
		Auth:           ProbeAuthOpenAI,
		Body:           "{}",
	},
	{
		ID:             "openai_responses",
		Method:         http.MethodPost,
		Path:           "/v1/responses",
		ProtocolFamily: ProtocolFamilyOpenAICompatible,
		Capability:     CapabilityResponses,
		Weight:         4,
		Auth:           ProbeAuthOpenAI,
		Body:           "{}",
	},
	{
		ID:             "anthropic_messages",
		Method:         http.MethodPost,
		Path:           "/v1/messages",
		ProtocolFamily: ProtocolFamilyAnthropicMessages,
		Capability:     CapabilityMessages,
		Weight:         4,
		Auth:           ProbeAuthAnthropic,
		Body:           "{}",
	},
	{
		ID:             "anthropic_models",
		Method:         http.MethodGet,
		Path:           "/v1/models",
		ProtocolFamily: ProtocolFamilyAnthropicMessages,
		Capability:     CapabilityModels,
		Weight:         1,
		Auth:           ProbeAuthAnthropic,
	},
	{
		ID:             "gemini_models",
		Method:         http.MethodGet,
		Path:           "/v1beta/models",
		ProtocolFamily: ProtocolFamilyGoogleGenAI,
		Capability:     CapabilityModels,
		Weight:         4,
		Auth:           ProbeAuthGemini,
	},
}

func ProviderProbeSpecs() []ProviderProbeSpec {
	out := make([]ProviderProbeSpec, len(defaultProviderProbeSpecs))
	copy(out, defaultProviderProbeSpecs)
	return out
}

func CapabilityFieldName(capability string) string {
	capability = normalizeSlug(capability)
	for _, spec := range capabilityRegistry {
		if spec.Name == capability {
			return spec.FieldName
		}
	}
	return ""
}

func SetCapabilityIfUnset(capabilities *config.UpstreamCapabilitiesConfig, capability string, enabled bool) bool {
	if capabilities == nil {
		return false
	}
	switch normalizeSlug(capability) {
	case CapabilityResponses:
		return setCapabilitySlotIfUnset(&capabilities.Responses, enabled)
	case CapabilityChatCompletions:
		return setCapabilitySlotIfUnset(&capabilities.ChatCompletions, enabled)
	case CapabilityToolCalling:
		return setCapabilitySlotIfUnset(&capabilities.ToolCalling, enabled)
	case CapabilityModels:
		return setCapabilitySlotIfUnset(&capabilities.Models, enabled)
	case CapabilityEmbeddings:
		return setCapabilitySlotIfUnset(&capabilities.Embeddings, enabled)
	case CapabilityTokenize:
		return setCapabilitySlotIfUnset(&capabilities.Tokenize, enabled)
	default:
		return false
	}
}

func setCapabilitySlotIfUnset(target **bool, enabled bool) bool {
	if target == nil || *target != nil {
		return false
	}
	value := enabled
	*target = &value
	return true
}

func SuggestedAPIType(protocolFamily string, capabilities []string) string {
	switch normalizeSlug(protocolFamily) {
	case ProtocolFamilyAnthropicMessages:
		return APITypeMessages
	case ProtocolFamilyGoogleGenAI, ProtocolFamilyVertexNative:
		return APITypeGemini
	case ProtocolFamilyOpenAICompatible:
		for _, capability := range capabilities {
			if normalizeSlug(capability) == CapabilityResponses {
				return APITypeResponses
			}
		}
		return APITypeChatCompletions
	default:
		return ""
	}
}

func AppendImpliedCapabilities(protocolFamily string, capabilities []string) []string {
	out := append([]string(nil), capabilities...)
	switch normalizeSlug(protocolFamily) {
	case ProtocolFamilyGoogleGenAI, ProtocolFamilyVertexNative:
		out = appendUniqueCapability(out, CapabilityGenerateContent)
	}
	sort.Strings(out)
	return out
}

func appendUniqueCapability(capabilities []string, capability string) []string {
	capability = strings.TrimSpace(capability)
	if capability == "" {
		return capabilities
	}
	for _, existing := range capabilities {
		if existing == capability {
			return capabilities
		}
	}
	return append(capabilities, capability)
}
