package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ExecutionEvent stores production audit events emitted while handling a Responses request.
type ExecutionEvent struct {
	ent.Schema
}

func (ExecutionEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "execution_events"}}
}

func (ExecutionEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("response_id").Optional(),
		field.String("request_audit_id").Optional(),
		field.String("conversation_id").Optional(),
		field.String("event_type").NotEmpty(),
		field.String("phase").Default(""),
		field.String("status").Default(""),
		field.String("message").Optional(),
		field.JSON("details_json", map[string]any{}).Optional(),
		field.Time("occurred_at").Default(time.Now).Immutable(),
	}
}

func (ExecutionEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("request_audit_id", "occurred_at"),
		index.Fields("response_id", "occurred_at"),
		index.Fields("conversation_id", "occurred_at"),
		index.Fields("event_type", "occurred_at"),
		index.Fields("phase", "status", "occurred_at"),
	}
}
