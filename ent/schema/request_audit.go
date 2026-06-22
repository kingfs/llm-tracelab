package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RequestAudit stores an incoming Responses request audit envelope.
type RequestAudit struct {
	ent.Schema
}

func (RequestAudit) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "request_audits"}}
}

func (RequestAudit) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("response_id").Optional(),
		field.String("conversation_id").Optional(),
		field.String("method").NotEmpty(),
		field.String("path").NotEmpty(),
		field.String("client_request_id").Optional(),
		field.JSON("header_json", map[string]any{}).Optional(),
		field.String("body_preview").Optional(),
		field.String("body_sha256").Optional(),
		field.JSON("redaction_json", map[string]any{}).Optional(),
		field.String("status").Default(""),
		field.String("error_text").Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (RequestAudit) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("response_id", "created_at"),
		index.Fields("conversation_id", "created_at"),
		index.Fields("client_request_id"),
		index.Fields("status", "created_at"),
	}
}
