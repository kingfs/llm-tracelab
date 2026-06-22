package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ToolCallAudit stores durable audit facts for hosted and server-side tool call lifecycles.
type ToolCallAudit struct {
	ent.Schema
}

func (ToolCallAudit) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "tool_call_audits"}}
}

func (ToolCallAudit) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("response_id").Optional(),
		field.String("request_audit_id").Optional(),
		field.String("conversation_id").Optional(),
		field.String("call_id").NotEmpty(),
		field.String("tool_type").NotEmpty(),
		field.String("tool_name").Optional(),
		field.String("executor").Optional(),
		field.String("status").Default(""),
		field.String("phase").Default("tool_call"),
		field.JSON("input_json", map[string]any{}).Optional(),
		field.JSON("output_json", map[string]any{}).Optional(),
		field.String("error_text").Optional(),
		field.JSON("metadata_json", map[string]any{}).Optional(),
		field.Time("started_at").Optional(),
		field.Time("completed_at").Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (ToolCallAudit) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("request_audit_id", "created_at"),
		index.Fields("response_id", "created_at"),
		index.Fields("conversation_id", "created_at"),
		index.Fields("call_id"),
		index.Fields("tool_name", "status", "created_at"),
		index.Fields("status", "created_at"),
	}
}
