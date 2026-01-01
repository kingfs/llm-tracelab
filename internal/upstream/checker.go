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
	"strings"
	"time"
)

// OpenAI Compatible Models Response Structure
type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// CheckConnectivity 调用上游 /v1/models 验证连通性
func CheckConnectivity(baseURL, apiKey string) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// 构造测试 URL
	targetURL := fmt.Sprintf("%s/v1/models", baseURL)
	if strings.HasSuffix(baseURL, "/") {
		targetURL = baseURL + "v1/models"
	}
	// 处理有些厂商 base_url 已经包含 /v1 的情况 (简单的容错)
	if strings.HasSuffix(baseURL, "/v1") || strings.HasSuffix(baseURL, "/v1/") {
		base := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1")
		targetURL = base + "/v1/models"
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return fmt.Errorf("create check request failed: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	slog.Info("Starting upstream connectivity check...", "url", targetURL)

	resp, err := client.Do(req)
	if err != nil {
		// 网络层面的错误，打印 Request 即可
		reqDump, _ := httputil.DumpRequestOut(req, false)
		slog.Error("Upstream check connection failed", "error", err)
		fmt.Printf("\n=== REQUEST DUMP ===\n%s\n====================\n", reqDump)
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
		fmt.Printf("\n=== FAILED INTERACTION ===\n")
		fmt.Printf("--- REQUEST ---\n")
		reqDump, _ := httputil.DumpRequestOut(req, false)
		fmt.Printf("%s\n", reqDump)

		fmt.Printf("--- RESPONSE ---\n")
		fmt.Printf("HTTP/1.1 %s\r\n", resp.Status)
		resp.Header.Write(os.Stdout)
		fmt.Printf("\r\n%s\n", string(bodyBytes))
		fmt.Printf("==========================\n")
		return fmt.Errorf("upstream status: %s", resp.Status)
	}

	// 尝试解析模型列表
	var payload modelsResponse
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		slog.Warn("Connectivity check passed, but failed to parse model list JSON", "error", err)
		// 依然视为成功，只是无法列出模型
	} else {
		slog.Info("Upstream connectivity check passed.")
		fmt.Println("\n=== AVAILABLE MODELS ===")
		if len(payload.Data) == 0 {
			fmt.Println("(No models returned in 'data' field)")
		}
		for _, m := range payload.Data {
			fmt.Printf("- %s\n", m.ID)
		}
		fmt.Println("========================")
	}

	return nil
}
