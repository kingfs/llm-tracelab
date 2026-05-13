package observe

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ParseStatusRecorded       ParseStatus = "recorded"
	ParseStatusIndexed        ParseStatus = "indexed"
	ParseStatusParseQueued    ParseStatus = "parse_queued"
	ParseStatusParsed         ParseStatus = "parsed"
	ParseStatusParseFailed    ParseStatus = "parse_failed"
	ParseStatusAnalysisQueued ParseStatus = "analysis_queued"
	ParseStatusAnalyzed       ParseStatus = "analyzed"
	ParseStatusAnalysisFailed ParseStatus = "analysis_failed"

	NodeInstruction      NormalizedType = "instruction"
	NodeMessage          NormalizedType = "message"
	NodeText             NormalizedType = "text"
	NodeReasoning        NormalizedType = "reasoning"
	NodeRefusal          NormalizedType = "refusal"
	NodeToolDeclaration  NormalizedType = "tool_declaration"
	NodeToolCall         NormalizedType = "tool_call"
	NodeToolCallDelta    NormalizedType = "tool_call_delta"
	NodeToolResult       NormalizedType = "tool_result"
	NodeServerToolCall   NormalizedType = "server_tool_call"
	NodeServerToolResult NormalizedType = "server_tool_result"
	NodeCode             NormalizedType = "code"
	NodeCodeResult       NormalizedType = "code_result"
	NodePatch            NormalizedType = "patch"
	NodeFile             NormalizedType = "file"
	NodeImage            NormalizedType = "image"
	NodeAudio            NormalizedType = "audio"
	NodeVideo            NormalizedType = "video"
	NodeCitation         NormalizedType = "citation"
	NodeSafety           NormalizedType = "safety"
	NodeUsage            NormalizedType = "usage"
	NodeError            NormalizedType = "error"
	NodeUnknown          NormalizedType = "unknown"

	ToolOwnerModelRequested   ToolOwner = "model_requested"
	ToolOwnerClientExecuted   ToolOwner = "client_executed"
	ToolOwnerProviderExecuted ToolOwner = "provider_executed"
	ToolOwnerInferred         ToolOwner = "inferred"
	ToolOwnerUnknown          ToolOwner = "unknown"

	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type ParseStatus string
type NormalizedType string
type ToolOwner string
type Severity string

type TraceObservation struct {
	TraceID       string         `json:"trace_id"`
	Provider      string         `json:"provider"`
	Operation     string         `json:"operation"`
	Endpoint      string         `json:"endpoint"`
	Model         string         `json:"model"`
	Parser        string         `json:"parser"`
	ParserVersion string         `json:"parser_version"`
	Status        ParseStatus    `json:"status"`
	Warnings      []ParseWarning `json:"warnings,omitempty"`

	Request  ObservationRequest  `json:"request"`
	Response ObservationResponse `json:"response"`
	Stream   ObservationStream   `json:"stream,omitempty"`
	Tools    ObservationTools    `json:"tools,omitempty"`
	Usage    ObservationUsage    `json:"usage,omitempty"`
	Timings  ObservationTimings  `json:"timings,omitempty"`
	Safety   ObservationSafety   `json:"safety,omitempty"`
	Findings []Finding           `json:"findings,omitempty"`
	RawRefs  RawReferences       `json:"raw_refs,omitempty"`
}

type ParseWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type SemanticNode struct {
	ID             string          `json:"id"`
	ProviderType   string          `json:"provider_type"`
	NormalizedType NormalizedType  `json:"normalized_type"`
	Role           string          `json:"role,omitempty"`
	Path           string          `json:"path"`
	Index          int             `json:"index,omitempty"`
	Text           string          `json:"text,omitempty"`
	JSON           json.RawMessage `json:"json,omitempty"`
	Raw            json.RawMessage `json:"raw,omitempty"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
	ParentID       string          `json:"parent_id,omitempty"`
	Children       []SemanticNode  `json:"children,omitempty"`
}

type FlatSemanticNode struct {
	Node     SemanticNode `json:"node"`
	ParentID string       `json:"parent_id,omitempty"`
	Depth    int          `json:"depth"`
}

type ObservationRequest struct {
	Instructions []SemanticNode `json:"instructions,omitempty"`
	Messages     []SemanticNode `json:"messages,omitempty"`
	Inputs       []SemanticNode `json:"inputs,omitempty"`
	Tools        []SemanticNode `json:"tools,omitempty"`
	Config       map[string]any `json:"config,omitempty"`
	Nodes        []SemanticNode `json:"nodes,omitempty"`
}

type ObservationResponse struct {
	Outputs     []SemanticNode `json:"outputs,omitempty"`
	Candidates  []SemanticNode `json:"candidates,omitempty"`
	ToolCalls   []SemanticNode `json:"tool_calls,omitempty"`
	ToolResults []SemanticNode `json:"tool_results,omitempty"`
	Reasoning   []SemanticNode `json:"reasoning,omitempty"`
	Refusals    []SemanticNode `json:"refusals,omitempty"`
	Safety      []SemanticNode `json:"safety,omitempty"`
	Errors      []SemanticNode `json:"errors,omitempty"`
	Nodes       []SemanticNode `json:"nodes,omitempty"`
}

type ObservationStream struct {
	Events               []StreamEvent  `json:"events,omitempty"`
	AccumulatedText      string         `json:"accumulated_text,omitempty"`
	AccumulatedReasoning string         `json:"accumulated_reasoning,omitempty"`
	AccumulatedToolCalls []SemanticNode `json:"accumulated_tool_calls,omitempty"`
	Errors               []SemanticNode `json:"errors,omitempty"`
}

type StreamEvent struct {
	Index          int             `json:"index"`
	EventType      string          `json:"event_type"`
	ProviderType   string          `json:"provider_type"`
	NormalizedType NormalizedType  `json:"normalized_type"`
	Path           string          `json:"path,omitempty"`
	Delta          string          `json:"delta,omitempty"`
	JSON           json.RawMessage `json:"json,omitempty"`
	At             time.Time       `json:"at,omitempty"`
}

type ObservationTools struct {
	Declarations []ToolDeclaration       `json:"declarations,omitempty"`
	Calls        []ToolCallObservation   `json:"calls,omitempty"`
	Results      []ToolResultObservation `json:"results,omitempty"`
}

type ToolDeclaration struct {
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind,omitempty"`
	Description  string          `json:"description,omitempty"`
	Schema       json.RawMessage `json:"schema,omitempty"`
	NodeID       string          `json:"node_id,omitempty"`
	Path         string          `json:"path,omitempty"`
	ProviderType string          `json:"provider_type,omitempty"`
}

type ToolCallObservation struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Owner    ToolOwner       `json:"owner"`
	ArgsText string          `json:"args_text,omitempty"`
	ArgsJSON json.RawMessage `json:"args_json,omitempty"`
	NodeID   string          `json:"node_id,omitempty"`
	Path     string          `json:"path,omitempty"`
}

type ToolResultObservation struct {
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Kind    string          `json:"kind,omitempty"`
	Owner   ToolOwner       `json:"owner"`
	Text    string          `json:"text,omitempty"`
	JSON    json.RawMessage `json:"json,omitempty"`
	NodeID  string          `json:"node_id,omitempty"`
	Path    string          `json:"path,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
}

type ObservationUsage struct {
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	TotalTokens         int `json:"total_tokens,omitempty"`
	ReasoningTokens     int `json:"reasoning_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
}

type ObservationTimings struct {
	StartedAt    time.Time `json:"started_at,omitempty"`
	CompletedAt  time.Time `json:"completed_at,omitempty"`
	DurationMs   int64     `json:"duration_ms,omitempty"`
	TTFTMs       int64     `json:"ttft_ms,omitempty"`
	TokensPerSec float64   `json:"tokens_per_sec,omitempty"`
}

type ObservationSafety struct {
	Blocked     bool            `json:"blocked,omitempty"`
	Refused     bool            `json:"refused,omitempty"`
	Categories  []SafetySignal  `json:"categories,omitempty"`
	ProviderRaw json.RawMessage `json:"provider_raw,omitempty"`
}

type SafetySignal struct {
	Category     string          `json:"category"`
	Probability  string          `json:"probability,omitempty"`
	Severity     string          `json:"severity,omitempty"`
	Blocked      bool            `json:"blocked,omitempty"`
	Path         string          `json:"path,omitempty"`
	ProviderType string          `json:"provider_type,omitempty"`
	Raw          json.RawMessage `json:"raw,omitempty"`
}

type Finding struct {
	ID              string    `json:"id"`
	TraceID         string    `json:"trace_id,omitempty"`
	Category        string    `json:"category"`
	Severity        Severity  `json:"severity"`
	Confidence      float64   `json:"confidence"`
	Title           string    `json:"title"`
	Description     string    `json:"description,omitempty"`
	EvidencePath    string    `json:"evidence_path"`
	EvidenceExcerpt string    `json:"evidence_excerpt,omitempty"`
	NodeID          string    `json:"node_id,omitempty"`
	Detector        string    `json:"detector"`
	DetectorVersion string    `json:"detector_version"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
}

type RawReferences struct {
	CassettePath  string `json:"cassette_path,omitempty"`
	RequestStart  int64  `json:"request_start,omitempty"`
	RequestEnd    int64  `json:"request_end,omitempty"`
	ResponseStart int64  `json:"response_start,omitempty"`
	ResponseEnd   int64  `json:"response_end,omitempty"`
}

func StableNodeID(section string, path string, providerType string, index int) string {
	key := fmt.Sprintf("%s|%s|%s|%d", section, path, providerType, index)
	sum := sha1.Sum([]byte(key))
	return "node_" + hex.EncodeToString(sum[:])[:16]
}

func EvidencePath(traceID string, node SemanticNode) string {
	parts := []string{"trace", traceID}
	if node.ID != "" {
		parts = append(parts, "node", node.ID)
	}
	if node.Path != "" {
		parts = append(parts, "path", node.Path)
	}
	return strings.Join(parts, "#")
}

func FlattenNodes(nodes []SemanticNode) []FlatSemanticNode {
	var out []FlatSemanticNode
	var walk func([]SemanticNode, string, int)
	walk = func(items []SemanticNode, parentID string, depth int) {
		for _, item := range items {
			item.ParentID = parentID
			children := item.Children
			item.Children = nil
			out = append(out, FlatSemanticNode{
				Node:     item,
				ParentID: parentID,
				Depth:    depth,
			})
			walk(children, item.ID, depth+1)
		}
	}
	walk(nodes, "", 0)
	return out
}

func RebuildNodeTree(rows []FlatSemanticNode) []SemanticNode {
	nodes := make(map[string]*SemanticNode, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		node := row.Node
		node.ParentID = row.ParentID
		node.Children = nil
		nodes[node.ID] = &node
		order = append(order, node.ID)
	}

	for _, id := range order {
		node := nodes[id]
		if node == nil {
			continue
		}
		if node.ParentID == "" || nodes[node.ParentID] == nil {
			continue
		}
		parent := nodes[node.ParentID]
		parent.Children = append(parent.Children, *node)
	}

	var roots []SemanticNode
	for _, id := range order {
		node := nodes[id]
		if node == nil {
			continue
		}
		if node.ParentID == "" || nodes[node.ParentID] == nil {
			roots = append(roots, *node)
		}
	}
	sortNodes(roots)
	return roots
}

func sortNodes(nodes []SemanticNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Index == nodes[j].Index {
			return nodes[i].ID < nodes[j].ID
		}
		return nodes[i].Index < nodes[j].Index
	})
	for i := range nodes {
		sortNodes(nodes[i].Children)
	}
}
