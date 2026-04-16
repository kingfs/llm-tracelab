package llm

import "encoding/json"

func parseProviderErrorResponse(body []byte) (LLMResponse, bool) {
	if resp, ok := parseOpenAIProviderError(body); ok {
		return resp, true
	}
	if resp, ok := parseAnthropicProviderError(body); ok {
		return resp, true
	}
	if resp, ok := parseGoogleProviderError(body); ok {
		return resp, true
	}
	return LLMResponse{}, false
}

func parseOpenAIProviderError(body []byte) (LLMResponse, bool) {
	var envelope struct {
		Error struct {
			Message string      `json:"message"`
			Type    string      `json:"type"`
			Code    interface{} `json:"code"`
			Param   interface{} `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return LLMResponse{}, false
	}
	if envelope.Error.Message == "" && envelope.Error.Type == "" && envelope.Error.Code == nil && envelope.Error.Param == nil {
		return LLMResponse{}, false
	}

	return providerErrorResponse(map[string]any{
		"provider": "openai_compatible",
		"message":  envelope.Error.Message,
		"type":     envelope.Error.Type,
		"code":     envelope.Error.Code,
		"param":    envelope.Error.Param,
	}), true
}

func parseAnthropicProviderError(body []byte) (LLMResponse, bool) {
	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return LLMResponse{}, false
	}
	if envelope.Type != "error" && envelope.Error.Message == "" && envelope.Error.Type == "" {
		return LLMResponse{}, false
	}

	return providerErrorResponse(map[string]any{
		"provider": "anthropic",
		"type":     envelope.Error.Type,
		"message":  envelope.Error.Message,
	}), true
}

func parseGoogleProviderError(body []byte) (LLMResponse, bool) {
	var envelope struct {
		Error struct {
			Code    int         `json:"code"`
			Message string      `json:"message"`
			Status  string      `json:"status"`
			Details interface{} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return LLMResponse{}, false
	}
	if envelope.Error.Code == 0 && envelope.Error.Message == "" && envelope.Error.Status == "" && envelope.Error.Details == nil {
		return LLMResponse{}, false
	}

	return providerErrorResponse(map[string]any{
		"provider": "google_genai",
		"code":     envelope.Error.Code,
		"message":  envelope.Error.Message,
		"status":   envelope.Error.Status,
		"details":  envelope.Error.Details,
	}), true
}

func providerErrorResponse(payload map[string]any) LLMResponse {
	return LLMResponse{
		Candidates: []LLMCandidate{{
			Role:         "assistant",
			FinishReason: "error",
		}},
		Extensions: map[string]any{
			"error": payload,
		},
	}
}

func parseOpenAIStreamError(jsonStr string) (map[string]any, bool) {
	var failed struct {
		Type     string `json:"type"`
		Response struct {
			Error struct {
				Message string      `json:"message"`
				Type    string      `json:"type"`
				Code    interface{} `json:"code"`
			} `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &failed); err == nil && failed.Type == "response.failed" {
		return map[string]any{
			"provider": "openai_compatible",
			"message":  failed.Response.Error.Message,
			"type":     failed.Response.Error.Type,
			"code":     failed.Response.Error.Code,
		}, true
	}

	resp, ok := parseOpenAIProviderError([]byte(jsonStr))
	if !ok {
		return nil, false
	}
	return resp.Extensions["error"].(map[string]any), true
}

func parseAnthropicStreamError(jsonStr string) (map[string]any, bool) {
	resp, ok := parseAnthropicProviderError([]byte(jsonStr))
	if !ok {
		return nil, false
	}
	return resp.Extensions["error"].(map[string]any), true
}

func parseGoogleStreamError(jsonStr string) (map[string]any, bool) {
	resp, ok := parseGoogleProviderError([]byte(jsonStr))
	if !ok {
		return nil, false
	}
	return resp.Extensions["error"].(map[string]any), true
}
