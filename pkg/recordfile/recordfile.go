package recordfile

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
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
	RequestID     string    `json:"request_id"`
	Time          time.Time `json:"time"`
	Model         string    `json:"model"`
	Provider      string    `json:"provider,omitempty"`
	Operation     string    `json:"operation,omitempty"`
	Endpoint      string    `json:"endpoint,omitempty"`
	URL           string    `json:"url"`
	Method        string    `json:"method"`
	StatusCode    int       `json:"status_code"`
	DurationMs    int64     `json:"duration_ms"`
	TTFTMs        int64     `json:"ttft_ms"`
	ClientIP      string    `json:"client_ip"`
	ContentLength int64     `json:"content_length"`
	Error         string    `json:"error,omitempty"`
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
	reader := bufio.NewReader(bytes.NewReader(content))
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read prelude: %w", err)
	}

	trimmed := strings.TrimRight(line, "\r\n")
	if trimmed == FileMagic {
		return parseV3Prelude(content)
	}

	var header RecordHeader
	if err := json.Unmarshal([]byte(line), &header); err != nil {
		return nil, fmt.Errorf("invalid record prelude: %w", err)
	}

	return &ParsedPrelude{
		Header:        header,
		PayloadOffset: LegacyHeaderLen,
	}, nil
}

func parseV3Prelude(content []byte) (*ParsedPrelude, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var (
		offset  int64
		header  RecordHeader
		events  []RecordEvent
		gotMeta bool
	)

	for scanner.Scan() {
		line := scanner.Text()
		offset += int64(len(scanner.Bytes())) + 1

		if line == FileMagic {
			continue
		}
		if line == "" {
			break
		}
		if strings.HasPrefix(line, metaPrefix) {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, metaPrefix)), &header); err != nil {
				return nil, fmt.Errorf("invalid v3 meta line: %w", err)
			}
			gotMeta = true
			continue
		}
		if strings.HasPrefix(line, eventPrefix) {
			var event RecordEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, eventPrefix)), &event); err != nil {
				return nil, fmt.Errorf("invalid v3 event line: %w", err)
			}
			events = append(events, event)
			continue
		}
		return nil, fmt.Errorf("invalid v3 prelude line: %q", line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan v3 prelude: %w", err)
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
