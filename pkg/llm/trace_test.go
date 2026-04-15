package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeEndpointSupportsOpenAICompatibleVariants(t *testing.T) {
	assert.Equal(t, "/v1/responses", NormalizeEndpoint("/openai/v1/responses?api-version=preview"))
	assert.Equal(t, "/v1/chat/completions", NormalizeEndpoint("/openai/deployments/gpt-4o/chat/completions"))
	assert.Equal(t, "/v1/models", NormalizeEndpoint("/v1/models"))
}

func TestClassifyPathSupportsDerivedOpenAIProviders(t *testing.T) {
	semantics := ClassifyPath("/openai/v1/responses?api-version=preview", "https://demo-resource.openai.azure.com/openai/v1")
	assert.Equal(t, ProviderAzureOpenAI, semantics.Provider)
	assert.Equal(t, OperationResponses, semantics.Operation)
	assert.Equal(t, "/v1/responses", semantics.Endpoint)

	semantics = ClassifyPath("/v1/chat/completions", "http://vllm.local:8000/v1")
	assert.Equal(t, ProviderVLLM, semantics.Provider)
	assert.Equal(t, OperationChatCompletions, semantics.Operation)
}
