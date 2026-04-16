package llm

import (
	"encoding/json"
	"strings"
)

type modelListItem struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type genericModelListEnvelope struct {
	Data             []modelListItem `json:"data"`
	Models           []modelListItem `json:"models"`
	PublisherModels  []modelListItem `json:"publisherModels"`
	ModelGardenModel []modelListItem `json:"modelGardenModels"`
}

func defaultModelListRequest() LLMRequest {
	return LLMRequest{
		Model: "list_models",
		Messages: []LLMMessage{{
			Role: "user",
			Content: []LLMContent{{
				Type: "text",
				Text: "List available models",
			}},
		}},
	}
}

func parseModelListResponse(body []byte) (LLMResponse, error) {
	if resp, ok := parseProviderErrorResponse(body); ok {
		return resp, nil
	}

	var payload genericModelListEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return LLMResponse{}, err
	}

	items := collectModelListItems(payload)
	lines := make([]string, 0, len(items))
	encodedItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		name := firstNonEmpty(item.ID, item.Name, item.DisplayName)
		if name == "" {
			continue
		}
		lines = append(lines, name)
		encodedItems = append(encodedItems, map[string]any{
			"id":           item.ID,
			"name":         item.Name,
			"display_name": item.DisplayName,
		})
	}

	text := strings.Join(lines, "\n")
	return LLMResponse{
		Candidates: []LLMCandidate{{
			Index:   0,
			Role:    "system",
			Content: []LLMContent{{Type: "text", Text: text}},
		}},
		Extensions: map[string]any{
			"model_list": encodedItems,
		},
	}, nil
}

func collectModelListItems(payload genericModelListEnvelope) []modelListItem {
	items := make([]modelListItem, 0, len(payload.Data)+len(payload.Models)+len(payload.PublisherModels)+len(payload.ModelGardenModel))
	items = append(items, payload.Data...)
	items = append(items, payload.Models...)
	items = append(items, payload.PublisherModels...)
	items = append(items, payload.ModelGardenModel...)
	return items
}
