package llm

import (
	"encoding/json"
	"fmt"
)

type Adapter interface {
	Semantics() TraceSemantics
	ParseRequest(body []byte) (LLMRequest, error)
	ParseResponse(body []byte) (LLMResponse, error)
	MarshalRequest(req LLMRequest) ([]byte, error)
	MarshalResponse(resp LLMResponse) ([]byte, error)
}

type StreamAdapter interface {
	ParseStreamResponse(body []byte) (LLMResponse, error)
}

type UnsupportedEndpointError struct {
	Provider string
	Endpoint string
}

func (e UnsupportedEndpointError) Error() string {
	return fmt.Sprintf("llm adapter not found for provider=%q endpoint=%q", e.Provider, e.Endpoint)
}

func AdapterFor(provider string, endpoint string) (Adapter, error) {
	semantics := TraceSemantics{
		Provider:  provider,
		Endpoint:  NormalizeEndpoint(endpoint),
		Operation: detectOperation(NormalizeEndpoint(endpoint), provider),
	}

	switch {
	case semantics.Endpoint == "/v1/chat/completions":
		return openAIChatAdapter{semantics: semantics}, nil
	case semantics.Endpoint == "/v1/responses":
		return openAIResponsesAdapter{semantics: semantics}, nil
	case semantics.Endpoint == "/v1/messages":
		return anthropicMessagesAdapter{semantics: semantics}, nil
	case semantics.Endpoint == "/v1beta/models:generateContent",
		semantics.Endpoint == "/v1beta/models:streamGenerateContent":
		return googleGenerateContentAdapter{semantics: semantics}, nil
	default:
		return nil, UnsupportedEndpointError{Provider: provider, Endpoint: semantics.Endpoint}
	}
}

func AdapterForPath(rawPath string, upstreamBaseURL string) (Adapter, error) {
	semantics := ClassifyPath(rawPath, upstreamBaseURL)
	return AdapterFor(semantics.Provider, semantics.Endpoint)
}

func ParseRequest(provider string, endpoint string, body []byte) (LLMRequest, error) {
	adapter, err := AdapterFor(provider, endpoint)
	if err != nil {
		return LLMRequest{}, err
	}
	return adapter.ParseRequest(body)
}

func ParseRequestForPath(rawPath string, upstreamBaseURL string, body []byte) (LLMRequest, error) {
	adapter, err := AdapterForPath(rawPath, upstreamBaseURL)
	if err != nil {
		return LLMRequest{}, err
	}
	req, err := adapter.ParseRequest(body)
	if err != nil {
		return LLMRequest{}, err
	}
	if req.Model == "" {
		req.Model = ModelFromPath(rawPath)
	}
	return req, nil
}

func ParseResponse(provider string, endpoint string, body []byte) (LLMResponse, error) {
	adapter, err := AdapterFor(provider, endpoint)
	if err != nil {
		return LLMResponse{}, err
	}
	return adapter.ParseResponse(body)
}

func ParseStreamResponse(provider string, endpoint string, body []byte) (LLMResponse, error) {
	adapter, err := AdapterFor(provider, endpoint)
	if err != nil {
		return LLMResponse{}, err
	}
	streamAdapter, ok := adapter.(StreamAdapter)
	if !ok {
		return LLMResponse{}, UnsupportedEndpointError{Provider: provider, Endpoint: endpoint}
	}
	return streamAdapter.ParseStreamResponse(body)
}

func ParseResponseForPath(rawPath string, upstreamBaseURL string, body []byte) (LLMResponse, error) {
	adapter, err := AdapterForPath(rawPath, upstreamBaseURL)
	if err != nil {
		return LLMResponse{}, err
	}
	return adapter.ParseResponse(body)
}

func ParseStreamResponseForPath(rawPath string, upstreamBaseURL string, body []byte) (LLMResponse, error) {
	adapter, err := AdapterForPath(rawPath, upstreamBaseURL)
	if err != nil {
		return LLMResponse{}, err
	}
	streamAdapter, ok := adapter.(StreamAdapter)
	if !ok {
		return LLMResponse{}, UnsupportedEndpointError{Provider: adapter.Semantics().Provider, Endpoint: adapter.Semantics().Endpoint}
	}
	return streamAdapter.ParseStreamResponse(body)
}

type openAIChatAdapter struct {
	semantics TraceSemantics
}

func (a openAIChatAdapter) Semantics() TraceSemantics { return a.semantics }
func (a openAIChatAdapter) ParseRequest(body []byte) (LLMRequest, error) {
	var req OpenAIChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return LLMRequest{}, err
	}
	return FromOpenAIRequest(req), nil
}
func (a openAIChatAdapter) ParseResponse(body []byte) (LLMResponse, error) {
	var resp OpenAIChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, err
	}
	return OpenAIToLLM(resp), nil
}
func (a openAIChatAdapter) MarshalRequest(req LLMRequest) ([]byte, error) {
	return json.Marshal(req.ToOpenAI())
}
func (a openAIChatAdapter) MarshalResponse(resp LLMResponse) ([]byte, error) {
	result := resp.ToOpenAIResponse()
	return json.Marshal(result)
}

type anthropicMessagesAdapter struct {
	semantics TraceSemantics
}

func (a anthropicMessagesAdapter) Semantics() TraceSemantics { return a.semantics }
func (a anthropicMessagesAdapter) ParseRequest(body []byte) (LLMRequest, error) {
	var req AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return LLMRequest{}, err
	}
	return FromAnthropicRequest(req), nil
}
func (a anthropicMessagesAdapter) ParseResponse(body []byte) (LLMResponse, error) {
	var resp AnthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, err
	}
	return AnthropicToLLM(resp), nil
}
func (a anthropicMessagesAdapter) MarshalRequest(req LLMRequest) ([]byte, error) {
	return json.Marshal(req.ToAnthropic())
}
func (a anthropicMessagesAdapter) MarshalResponse(resp LLMResponse) ([]byte, error) {
	result := resp.ToAnthropicResponse()
	return json.Marshal(result)
}

type googleGenerateContentAdapter struct {
	semantics TraceSemantics
}

func (a googleGenerateContentAdapter) Semantics() TraceSemantics { return a.semantics }
func (a googleGenerateContentAdapter) ParseRequest(body []byte) (LLMRequest, error) {
	var req GeminiGenerateContentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return LLMRequest{}, err
	}
	return FromGeminiRequest(req), nil
}
func (a googleGenerateContentAdapter) ParseResponse(body []byte) (LLMResponse, error) {
	var resp GeminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, err
	}
	return GeminiToLLM(resp), nil
}
func (a googleGenerateContentAdapter) MarshalRequest(req LLMRequest) ([]byte, error) {
	return json.Marshal(req.ToGemini())
}
func (a googleGenerateContentAdapter) MarshalResponse(resp LLMResponse) ([]byte, error) {
	return json.Marshal(resp.ToGeminiResponse())
}
