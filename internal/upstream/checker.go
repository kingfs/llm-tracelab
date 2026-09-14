package upstream

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAI Compatible Models Response Structure
type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Models []struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	} `json:"models"`
}

func DiscoverModelsResolved(resolved ResolvedUpstream, client *http.Client) ([]string, error) {
	if client == nil {
		client = defaultConnectivityHTTPClient()
	}
	targetURL, err := resolved.ConnectivityCheckURL()
	if err != nil {
		return nil, fmt.Errorf("build check url failed: %w", err)
	}
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create check request failed: %w", err)
	}
	resolved.ApplyAuthHeaders(req.Header)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream status: %s", resp.Status)
	}
	return extractModelNames(bodyBytes)
}

func defaultConnectivityHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
	}
}

func extractModelNames(body []byte) ([]string, error) {
	var payload modelsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(payload.Data)+len(payload.Models))
	for _, item := range payload.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	for _, item := range payload.Models {
		switch {
		case item.Name != "":
			models = append(models, item.Name)
		case item.ID != "":
			models = append(models, item.ID)
		}
	}
	return models, nil
}
