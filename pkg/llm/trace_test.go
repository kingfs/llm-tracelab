package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeEndpointSupportsOpenAICompatibleVariants(t *testing.T) {
	assert.Equal(t, "/v1/responses", NormalizeEndpoint("/openai/v1/responses?api-version=preview"))
	assert.Equal(t, "/v1/chat/completions", NormalizeEndpoint("/openai/deployments/gpt-4o/chat/completions"))
	assert.Equal(t, "/v1/models", NormalizeEndpoint("/v1/models"))
	assert.Equal(t, "/v1beta/models:generateContent", NormalizeEndpoint("/v1beta/models/gemini-2.5-flash:generateContent"))
	assert.Equal(t, "/v1/publishers/models:generateContent", NormalizeEndpoint("/v1/projects/demo/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent"))
}

func TestClassifyPathSupportsDerivedOpenAIProviders(t *testing.T) {
	semantics := ClassifyPath("/openai/v1/responses?api-version=preview", "https://demo-resource.openai.azure.com/openai/v1")
	assert.Equal(t, ProviderAzureOpenAI, semantics.Provider)
	assert.Equal(t, OperationResponses, semantics.Operation)
	assert.Equal(t, "/v1/responses", semantics.Endpoint)

	semantics = ClassifyPath("/v1/chat/completions", "http://vllm.local:8000/v1")
	assert.Equal(t, ProviderVLLM, semantics.Provider)
	assert.Equal(t, OperationChatCompletions, semantics.Operation)

	semantics = ClassifyPath("/v1beta/models/gemini-2.5-flash:generateContent", "https://generativelanguage.googleapis.com")
	assert.Equal(t, ProviderGoogleGenAI, semantics.Provider)
	assert.Equal(t, OperationGenerateContent, semantics.Operation)
	assert.Equal(t, "/v1beta/models:generateContent", semantics.Endpoint)

	semantics = ClassifyPath("/v1/projects/demo/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent", "https://us-central1-aiplatform.googleapis.com")
	assert.Equal(t, ProviderVertexNative, semantics.Provider)
	assert.Equal(t, OperationGenerateContent, semantics.Operation)
	assert.Equal(t, "/v1/publishers/models:generateContent", semantics.Endpoint)
}

func TestModelFromPath(t *testing.T) {
	assert.Equal(t, "gemini-2.5-flash", ModelFromPath("/v1beta/models/gemini-2.5-flash:generateContent"))
	assert.Equal(t, "gemini-2.5-flash", ModelFromPath("/v1/projects/demo/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent"))
	assert.Equal(t, "", ModelFromPath("/v1/messages"))
}
