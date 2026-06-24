package recordfile

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	FileMagic       = "# llm-tracelab/v3"
	metaPrefix      = "# meta: "
	eventPrefix     = "# event: "
	LegacyHeaderLen = 2048
)

type PromptTokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type UsageInfo struct {
	PromptTokens       int                 `json:"prompt_tokens"`
	CompletionTokens   int                 `json:"completion_tokens"`
	TotalTokens        int                 `json:"total_tokens"`
	PromptTokenDetails *PromptTokenDetails `json:"prompt_tokens_details,omitempty"`
}

type LayoutInfo struct {
	ReqHeaderLen int64 `json:"req_header_len"`
	ReqBodyLen   int64 `json:"req_body_len"`
	ResHeaderLen int64 `json:"res_header_len"`
	ResBodyLen   int64 `json:"res_body_len"`
	IsStream     bool  `json:"is_stream"`
}

type MetaData struct {
	RequestID                      string    `json:"request_id"`
	RequestAuditID                 string    `json:"request_audit_id,omitempty"`
	ResponseID                     string    `json:"response_id,omitempty"`
	ConversationID                 string    `json:"conversation_id,omitempty"`
	ClientRequestID                string    `json:"client_request_id,omitempty"`
	ExchangeID                     string    `json:"exchange_id,omitempty"`
	ExchangeKind                   string    `json:"exchange_kind,omitempty"`
	ExchangeRole                   string    `json:"exchange_role,omitempty"`
	ParentExchangeID               string    `json:"parent_exchange_id,omitempty"`
	SequenceIndex                  int       `json:"sequence_index,omitempty"`
	TraceID                        string    `json:"trace_id,omitempty"`
	Time                           time.Time `json:"time"`
	Model                          string    `json:"model"`
	Provider                       string    `json:"provider,omitempty"`
	Operation                      string    `json:"operation,omitempty"`
	Endpoint                       string    `json:"endpoint,omitempty"`
	URL                            string    `json:"url"`
	Method                         string    `json:"method"`
	StatusCode                     int       `json:"status_code"`
	DurationMs                     int64     `json:"duration_ms"`
	TTFTMs                         int64     `json:"ttft_ms"`
	ClientIP                       string    `json:"client_ip"`
	ContentLength                  int64     `json:"content_length"`
	Error                          string    `json:"error,omitempty"`
	SelectedUpstreamID             string    `json:"selected_upstream_id,omitempty"`
	SelectedUpstreamBaseURL        string    `json:"selected_upstream_base_url,omitempty"`
	SelectedUpstreamProviderPreset string    `json:"selected_upstream_provider_preset,omitempty"`
	RoutingPolicy                  string    `json:"routing_policy,omitempty"`
	RoutingScore                   float64   `json:"routing_score,omitempty"`
	RoutingCandidateCount          int       `json:"routing_candidate_count,omitempty"`
	RoutingFailureReason           string    `json:"routing_failure_reason,omitempty"`
}

type RecordHeader struct {
	Version string     `json:"version"`
	Meta    MetaData   `json:"meta"`
	Layout  LayoutInfo `json:"layout"`
	Usage   UsageInfo  `json:"usage"`
}

type RecordEvent struct {
	Type        string                 `json:"type"`
	Time        time.Time              `json:"time,omitempty"`
	Method      string                 `json:"method,omitempty"`
	URL         string                 `json:"url,omitempty"`
	StatusCode  int                    `json:"status_code,omitempty"`
	IsStream    bool                   `json:"is_stream,omitempty"`
	HeaderBytes int64                  `json:"header_bytes,omitempty"`
	BodyBytes   int64                  `json:"body_bytes,omitempty"`
	Message     string                 `json:"message,omitempty"`
	Attributes  map[string]interface{} `json:"attributes,omitempty"`
}

type ParsedPrelude struct {
	Header        RecordHeader
	Events        []RecordEvent
	PayloadOffset int64
}

type CassetteSummaryOptions struct {
	BodyLimit int
}

type HTTPRequestSummary struct {
	Method        string              `json:"method"`
	URL           string              `json:"url"`
	Header        map[string][]string `json:"header,omitempty"`
	Body          string              `json:"body,omitempty"`
	BodyBytes     int                 `json:"body_bytes"`
	BodySHA256    string              `json:"body_sha256,omitempty"`
	BodyTruncated bool                `json:"body_truncated"`
}

type HTTPResponseSummary struct {
	Status        string              `json:"status"`
	StatusCode    int                 `json:"status_code"`
	ContentType   string              `json:"content_type,omitempty"`
	Header        map[string][]string `json:"header,omitempty"`
	Body          string              `json:"body,omitempty"`
	BodyBytes     int                 `json:"body_bytes"`
	BodySHA256    string              `json:"body_sha256,omitempty"`
	BodyTruncated bool                `json:"body_truncated"`
	IsStream      bool                `json:"is_stream"`
}

type HTTPExchangeSummary struct {
	Header   RecordHeader        `json:"header"`
	Events   []RecordEvent       `json:"events,omitempty"`
	Request  HTTPRequestSummary  `json:"request"`
	Response HTTPResponseSummary `json:"response"`
}

func MarshalPrelude(header RecordHeader, events []RecordEvent) ([]byte, error) {
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString(FileMagic)
	buf.WriteByte('\n')
	buf.WriteString(metaPrefix)
	buf.Write(headerJSON)
	buf.WriteByte('\n')

	for _, event := range events {
		eventJSON, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		buf.WriteString(eventPrefix)
		buf.Write(eventJSON)
		buf.WriteByte('\n')
	}

	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func BuildEvents(header RecordHeader) []RecordEvent {
	events := []RecordEvent{
		{
			Type:        "request",
			Time:        header.Meta.Time,
			Method:      header.Meta.Method,
			URL:         header.Meta.URL,
			HeaderBytes: header.Layout.ReqHeaderLen,
			BodyBytes:   header.Layout.ReqBodyLen,
		},
		{
			Type:        "response",
			Time:        header.Meta.Time.Add(time.Duration(header.Meta.DurationMs) * time.Millisecond),
			StatusCode:  header.Meta.StatusCode,
			IsStream:    header.Layout.IsStream,
			HeaderBytes: header.Layout.ResHeaderLen,
			BodyBytes:   header.Layout.ResBodyLen,
		},
	}

	if header.Meta.Error != "" {
		events = append(events, RecordEvent{
			Type:    "error",
			Time:    header.Meta.Time.Add(time.Duration(header.Meta.DurationMs) * time.Millisecond),
			Message: header.Meta.Error,
		})
	}

	return events
}

func ParsePrelude(content []byte) (*ParsedPrelude, error) {
	lineEnd := bytes.IndexByte(content, '\n')
	if lineEnd < 0 {
		return nil, fmt.Errorf("failed to read prelude: missing first line")
	}

	line := bytes.TrimSuffix(content[:lineEnd], []byte("\r"))
	if bytes.Equal(line, []byte(FileMagic)) {
		return parseV3Prelude(content)
	}

	var header RecordHeader
	if err := json.Unmarshal(line, &header); err != nil {
		return nil, fmt.Errorf("invalid record prelude: %w", err)
	}

	return &ParsedPrelude{
		Header:        header,
		PayloadOffset: LegacyHeaderLen,
	}, nil
}

func parseV3Prelude(content []byte) (*ParsedPrelude, error) {
	var (
		offset  int64
		header  RecordHeader
		events  []RecordEvent
		gotMeta bool
	)

	for len(content) > 0 {
		lineEnd := bytes.IndexByte(content, '\n')
		if lineEnd < 0 {
			return nil, fmt.Errorf("scan v3 prelude: missing blank line")
		}

		rawLine := content[:lineEnd]
		line := bytes.TrimSuffix(rawLine, []byte("\r"))
		offset += int64(lineEnd + 1)
		content = content[lineEnd+1:]

		if bytes.Equal(line, []byte(FileMagic)) {
			continue
		}
		if len(line) == 0 {
			break
		}
		if bytes.HasPrefix(line, []byte(metaPrefix)) {
			if err := json.Unmarshal(line[len(metaPrefix):], &header); err != nil {
				return nil, fmt.Errorf("invalid v3 meta line: %w", err)
			}
			gotMeta = true
			continue
		}
		if bytes.HasPrefix(line, []byte(eventPrefix)) {
			var event RecordEvent
			if err := json.Unmarshal(line[len(eventPrefix):], &event); err != nil {
				return nil, fmt.Errorf("invalid v3 event line: %w", err)
			}
			events = append(events, event)
			continue
		}
		return nil, fmt.Errorf("invalid v3 prelude line: %q", string(line))
	}

	if !gotMeta {
		return nil, fmt.Errorf("missing v3 meta line")
	}

	return &ParsedPrelude{
		Header:        header,
		Events:        events,
		PayloadOffset: offset,
	}, nil
}

func ExtractSections(content []byte, parsed *ParsedPrelude) (reqFull, reqBody, resFull, resBody []byte) {
	if parsed == nil {
		return nil, nil, nil, nil
	}

	payloadOffset := parsed.PayloadOffset
	if payloadOffset > int64(len(content)) {
		payloadOffset = int64(len(content))
	}

	reqStart := payloadOffset
	reqEnd := reqStart + parsed.Header.Layout.ReqHeaderLen + parsed.Header.Layout.ReqBodyLen
	if reqEnd > int64(len(content)) {
		reqEnd = int64(len(content))
	}
	if reqStart < reqEnd {
		reqFull = content[reqStart:reqEnd]
	}

	reqBodyStart := reqStart + parsed.Header.Layout.ReqHeaderLen
	if reqBodyStart < reqEnd {
		reqBody = content[reqBodyStart:reqEnd]
	}

	resStart := reqEnd + 1
	if resStart > int64(len(content)) {
		resStart = int64(len(content))
	}
	if resStart < int64(len(content)) {
		resFull = content[resStart:]
	}

	resBodyStart := resStart + parsed.Header.Layout.ResHeaderLen
	if resBodyStart < int64(len(content)) {
		resBody = content[resBodyStart:]
	}

	return reqFull, reqBody, resFull, resBody
}

func SummarizeHTTPExchange(content []byte, opts CassetteSummaryOptions) (*HTTPExchangeSummary, error) {
	parsed, err := ParsePrelude(content)
	if err != nil {
		return nil, err
	}
	reqFull, reqBody, resFull, resBody := ExtractSections(content, parsed)

	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqFull)))
	if err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(resFull)), req)
	if err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &HTTPExchangeSummary{
		Header: parsed.Header,
		Events: append([]RecordEvent(nil), parsed.Events...),
		Request: HTTPRequestSummary{
			Method:        req.Method,
			URL:           req.URL.String(),
			Header:        cloneHeader(req.Header),
			Body:          summarizeBody(reqBody, normalizeBodyLimit(opts.BodyLimit)),
			BodyBytes:     len(reqBody),
			BodySHA256:    bodySHA256(reqBody),
			BodyTruncated: len(reqBody) > normalizeBodyLimit(opts.BodyLimit),
		},
		Response: HTTPResponseSummary{
			Status:        resp.Status,
			StatusCode:    resp.StatusCode,
			ContentType:   resp.Header.Get("Content-Type"),
			Header:        cloneHeader(resp.Header),
			Body:          summarizeBody(resBody, normalizeBodyLimit(opts.BodyLimit)),
			BodyBytes:     len(resBody),
			BodySHA256:    bodySHA256(resBody),
			BodyTruncated: len(resBody) > normalizeBodyLimit(opts.BodyLimit),
			IsStream:      parsed.Header.Layout.IsStream,
		},
	}, nil
}

func normalizeBodyLimit(limit int) int {
	if limit <= 0 {
		return 4096
	}
	if limit > 20000 {
		return 20000
	}
	return limit
}

func summarizeBody(body []byte, limit int) string {
	if len(body) == 0 {
		return ""
	}
	if len(body) > limit {
		return string(body[:limit])
	}
	return string(body)
}

func bodySHA256(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func cloneHeader(header http.Header) map[string][]string {
	out := make(map[string][]string, len(header))
	for key, values := range header {
		out[key] = append([]string(nil), values...)
	}
	return out
}
