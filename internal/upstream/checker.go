package upstream

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"os"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
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

// CheckConnectivity 调用上游模型列表 endpoint 验证连通性
func CheckConnectivity(cfg config.UpstreamConfig) error {
	return checkConnectivity(cfg, defaultConnectivityHTTPClient(), os.Stdout)
}

func checkConnectivity(cfg config.UpstreamConfig, client *http.Client, stdout io.Writer) error {
	resolved, err := Resolve(cfg)
	if err != nil {
		return err
	}

	targetURL, err := resolved.ConnectivityCheckURL()
	if err != nil {
		return fmt.Errorf("build check url failed: %w", err)
	}
	diagnostics, err := resolved.StartupDiagnostics()
	if err != nil {
		return fmt.Errorf("build startup diagnostics failed: %w", err)
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return fmt.Errorf("create check request failed: %w", err)
	}

	resolved.ApplyAuthHeaders(req.Header)
	req.Header.Set("Content-Type", "application/json")

	slog.Info(
		"Starting upstream connectivity check...",
		"url", targetURL,
		"connectivity_endpoint", diagnostics.ConnectivityEndpoint,
		"provider_preset", resolved.ProviderPreset,
		"protocol_family", resolved.ProtocolFamily,
		"routing_profile", resolved.RoutingProfile,
		"model_routing_hint", diagnostics.ModelRoutingHint,
	)

	resp, err := client.Do(req)
	if err != nil {
		// 网络层面的错误，打印 Request 即可
		reqDump, _ := httputil.DumpRequestOut(req, false)
		slog.Error("Upstream check connection failed", "error", err)
		fmt.Fprintf(stdout, "\n=== REQUEST DUMP ===\n%s\n====================\n", reqDump)
		return err
	}
	defer resp.Body.Close()

	// 读取 Body 内容用于后续解析和 Dump
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body failed: %w", err)
	}

	// 检查状态码
	if resp.StatusCode != 200 {
		slog.Error("Upstream check returned non-200 status", "status", resp.Status)

		// 重新构造用于打印的 Response Dump（因为 Body 已经被读出来了）
		fmt.Fprintf(stdout, "\n=== FAILED INTERACTION ===\n")
		fmt.Fprintf(stdout, "--- REQUEST ---\n")
		reqDump, _ := httputil.DumpRequestOut(req, false)
		fmt.Fprintf(stdout, "%s\n", reqDump)

		fmt.Fprintf(stdout, "--- RESPONSE ---\n")
		fmt.Fprintf(stdout, "HTTP/1.1 %s\r\n", resp.Status)
		resp.Header.Write(stdout)
		fmt.Fprintf(stdout, "\r\n%s\n", string(bodyBytes))
		fmt.Fprintf(stdout, "==========================\n")
		return fmt.Errorf("upstream status: %s", resp.Status)
	}

	// 尝试解析模型列表
	models, err := extractModelNames(bodyBytes)
	if err != nil {
		slog.Warn("Connectivity check passed, but failed to parse model list JSON", "error", err)
		// 依然视为成功，只是无法列出模型
	} else {
		slog.Info("Upstream connectivity check passed.")
		fmt.Fprintln(stdout, "\n=== AVAILABLE MODELS ===")
		if len(models) == 0 {
			fmt.Fprintln(stdout, "(No models returned in 'data' field)")
		}
		for _, model := range models {
			fmt.Fprintf(stdout, "- %s\n", model)
		}
		fmt.Fprintln(stdout, "========================")
	}

	return nil
}

func defaultConnectivityHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
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
