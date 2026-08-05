package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Response stores a checkpoint for OpenAI-compatible /v1/responses state.
type Response struct {
	ent.Schema
}

func (Response) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "responses"}}
}

func (Response) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("conversation_id").Default(""),
		field.String("previous_response_id").Default(""),
		field.Enum("status").Values("queued", "in_progress", "completed", "failed", "incomplete", "cancelled").Default("queued"),
		field.String("model").NotEmpty(),
		field.JSON("history_item_ids", []string{}).Optional(),
		field.JSON("output_item_ids", []string{}).Optional(),
		field.JSON("effective_tools", []map[string]any{}).Optional(),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.JSON("usage", map[string]any{}).Optional(),
		field.JSON("error", map[string]any{}).Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Response) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("conversation_id", "created_at"),
		index.Fields("previous_response_id"),
		index.Fields("status", "created_at"),
	}
}
