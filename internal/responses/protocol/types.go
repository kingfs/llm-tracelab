package protocol

import "encoding/json"

type CreateResponseRequest struct {
	Model              string         `json:"model,omitempty"`
	Input              any            `json:"input"`
	Instructions       string         `json:"instructions,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Store              *bool          `json:"store,omitempty"`
	Stream             bool           `json:"stream,omitempty"`
	Tools              []Tool         `json:"tools,omitempty"`
	ToolChoice         any            `json:"tool_choice,omitempty"`
	MaxOutputTokens    int            `json:"max_output_tokens,omitempty"`
	Temperature        *float64       `json:"temperature,omitempty"`
	TopP               *float64       `json:"top_p,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	Extra              map[string]any `json:"-"`
}

func (r *CreateResponseRequest) UnmarshalJSON(data []byte) error {
	type requestAlias CreateResponseRequest
	var alias requestAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{
		"model",
		"input",
		"instructions",
		"previous_response_id",
		"store",
		"stream",
		"tools",
		"tool_choice",
		"max_output_tokens",
		"temperature",
		"top_p",
		"metadata",
	} {
		delete(raw, key)
	}
	*r = CreateResponseRequest(alias)
	r.Extra = nil
	if len(raw) > 0 {
		r.Extra = make(map[string]any, len(raw))
		for key, value := range raw {
			var decoded any
			if err := json.Unmarshal(value, &decoded); err != nil {
				return err
			}
			r.Extra[key] = decoded
		}
	}
	return nil
}

func (r CreateResponseRequest) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(r.Extra)+12)
	for key, value := range r.Extra {
		out[key] = value
	}
	if r.Model != "" {
		out["model"] = r.Model
	}
	if r.Input != nil {
		out["input"] = r.Input
	}
	if r.Instructions != "" {
		out["instructions"] = r.Instructions
	}
	if r.PreviousResponseID != "" {
		out["previous_response_id"] = r.PreviousResponseID
	}
	if r.Store != nil {
		out["store"] = r.Store
	}
	if r.Stream {
		out["stream"] = r.Stream
	}
	if len(r.Tools) > 0 {
		out["tools"] = r.Tools
	}
	if r.ToolChoice != nil {
		out["tool_choice"] = r.ToolChoice
	}
	if r.MaxOutputTokens != 0 {
		out["max_output_tokens"] = r.MaxOutputTokens
	}
	if r.Temperature != nil {
		out["temperature"] = r.Temperature
	}
	if r.TopP != nil {
		out["top_p"] = r.TopP
	}
	if r.Metadata != nil {
		out["metadata"] = r.Metadata
	}
	return json.Marshal(out)
}

type CompactResponseRequest struct {
	ResponseID string         `json:"response_id"`
	Model      string         `json:"model,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type Tool struct {
	Type            string         `json:"type"`
	Name            string         `json:"name,omitempty"`
	Description     string         `json:"description,omitempty"`
	Parameters      map[string]any `json:"parameters,omitempty"`
	ServerLabel     string         `json:"server_label,omitempty"`
	AllowedTools    any            `json:"allowed_tools,omitempty"`
	RequireApproval any            `json:"require_approval,omitempty"`
	Filters         any            `json:"filters,omitempty"`
	MaxNumResults   int            `json:"max_num_results,omitempty"`
	UserLocation    any            `json:"user_location,omitempty"`
	Extra           map[string]any `json:"-"`
}

func (t *Tool) UnmarshalJSON(data []byte) error {
	type toolAlias Tool
	var alias toolAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{
		"type",
		"name",
		"description",
		"parameters",
		"server_label",
		"allowed_tools",
		"require_approval",
		"filters",
		"max_num_results",
		"user_location",
	} {
		delete(raw, key)
	}
	*t = Tool(alias)
	t.Extra = nil
	if len(raw) > 0 {
		t.Extra = make(map[string]any, len(raw))
		for key, value := range raw {
			var decoded any
			if err := json.Unmarshal(value, &decoded); err != nil {
				return err
			}
			t.Extra[key] = decoded
		}
	}
	return nil
}

func (t Tool) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(t.Extra)+10)
	for key, value := range t.Extra {
		out[key] = value
	}
	out["type"] = t.Type
	if t.Name != "" {
		out["name"] = t.Name
	}
	if t.Description != "" {
		out["description"] = t.Description
	}
	if t.Parameters != nil {
		out["parameters"] = t.Parameters
	}
	if t.ServerLabel != "" {
		out["server_label"] = t.ServerLabel
	}
	if t.AllowedTools != nil {
		out["allowed_tools"] = t.AllowedTools
	}
	if t.RequireApproval != nil {
		out["require_approval"] = t.RequireApproval
	}
	if t.Filters != nil {
		out["filters"] = t.Filters
	}
	if t.MaxNumResults != 0 {
		out["max_num_results"] = t.MaxNumResults
	}
	if t.UserLocation != nil {
		out["user_location"] = t.UserLocation
	}
	return json.Marshal(out)
}

type Response struct {
	ID                 string         `json:"id"`
	Object             string         `json:"object"`
	CreatedAt          int64          `json:"created_at"`
	Status             string         `json:"status"`
	Model              string         `json:"model"`
	Output             []OutputItem   `json:"output"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Usage              Usage          `json:"usage"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

type OutputItem struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Status    string         `json:"status,omitempty"`
	Role      string         `json:"role,omitempty"`
	Content   []ContentPart  `json:"content,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments string         `json:"arguments,omitempty"`
	Output    any            `json:"output,omitempty"`
	Action    map[string]any `json:"action,omitempty"`
	Extra     map[string]any `json:"-"`
}

func (o *OutputItem) UnmarshalJSON(data []byte) error {
	type outputItemAlias OutputItem
	var alias outputItemAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{
		"id",
		"type",
		"status",
		"role",
		"content",
		"call_id",
		"name",
		"arguments",
		"output",
		"action",
	} {
		delete(raw, key)
	}
	*o = OutputItem(alias)
	o.Extra = decodeExtra(raw)
	return nil
}

func (o OutputItem) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(o.Extra)+10)
	for key, value := range o.Extra {
		out[key] = value
	}
	if o.ID != "" {
		out["id"] = o.ID
	}
	if o.Type != "" {
		out["type"] = o.Type
	}
	if o.Status != "" {
		out["status"] = o.Status
	}
	if o.Role != "" {
		out["role"] = o.Role
	}
	if len(o.Content) > 0 {
		out["content"] = o.Content
	}
	if o.CallID != "" {
		out["call_id"] = o.CallID
	}
	if o.Name != "" {
		out["name"] = o.Name
	}
	if o.Arguments != "" {
		out["arguments"] = o.Arguments
	}
	if o.Output != nil {
		out["output"] = o.Output
	}
	if o.Action != nil {
		out["action"] = o.Action
	}
	return json.Marshal(out)
}

type InputItem struct {
	ID        string         `json:"id,omitempty"`
	Type      string         `json:"type"`
	Role      string         `json:"role,omitempty"`
	Content   []ContentPart  `json:"content,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments string         `json:"arguments,omitempty"`
	Output    any            `json:"output,omitempty"`
	Extra     map[string]any `json:"-"`
}

func (i *InputItem) UnmarshalJSON(data []byte) error {
	type inputItemAlias InputItem
	var alias inputItemAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{
		"id",
		"type",
		"role",
		"content",
		"call_id",
		"tool_call_id",
		"name",
		"arguments",
		"output",
	} {
		delete(raw, key)
	}
	*i = InputItem(alias)
	if i.CallID == "" {
		var toolCallID string
		if rawToolCallID, ok := raw["tool_call_id"]; ok {
			_ = json.Unmarshal(rawToolCallID, &toolCallID)
		}
		i.CallID = toolCallID
	}
	i.Extra = decodeExtra(raw)
	return nil
}

func (i InputItem) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(i.Extra)+8)
	for key, value := range i.Extra {
		out[key] = value
	}
	if i.ID != "" {
		out["id"] = i.ID
	}
	if i.Type != "" {
		out["type"] = i.Type
	}
	if i.Role != "" {
		out["role"] = i.Role
	}
	if len(i.Content) > 0 {
		out["content"] = i.Content
	}
	if i.CallID != "" {
		out["call_id"] = i.CallID
	}
	if i.Name != "" {
		out["name"] = i.Name
	}
	if i.Arguments != "" {
		out["arguments"] = i.Arguments
	}
	if i.Output != nil {
		out["output"] = i.Output
	}
	return json.Marshal(out)
}

type InputItemList struct {
	Object  string      `json:"object"`
	Data    []InputItem `json:"data"`
	FirstID string      `json:"first_id,omitempty"`
	LastID  string      `json:"last_id,omitempty"`
	HasMore bool        `json:"has_more"`
}

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code"`
}

type StreamEvent struct {
	Type         string       `json:"type"`
	Response     *Response    `json:"response,omitempty"`
	Item         *OutputItem  `json:"item,omitempty"`
	OutputIndex  *int         `json:"output_index,omitempty"`
	ItemID       string       `json:"item_id,omitempty"`
	ContentIndex *int         `json:"content_index,omitempty"`
	Part         *ContentPart `json:"part,omitempty"`
	Delta        string       `json:"delta,omitempty"`
	Text         string       `json:"text,omitempty"`
	Arguments    string       `json:"arguments,omitempty"`
	Error        *ErrorBody   `json:"error,omitempty"`
}

func decodeExtra(raw map[string]json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	extra := make(map[string]any, len(raw))
	for key, value := range raw {
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			continue
		}
		extra[key] = decoded
	}
	return extra
}
