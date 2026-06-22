package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UpstreamExchange stores audit metadata for a provider exchange triggered by Responses.
type UpstreamExchange struct {
	ent.Schema
}

func (UpstreamExchange) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "upstream_exchanges"}}
}

func (UpstreamExchange) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("response_id").Optional(),
		field.String("request_audit_id").Optional(),
		field.String("trace_id").Optional(),
		field.String("cassette_path").Optional(),
		field.String("upstream_id").Optional(),
		field.String("route_target").Optional(),
		field.String("model").Optional(),
		field.String("endpoint").Optional(),
		field.Int("status_code").Optional(),
		field.Time("started_at").Optional(),
		field.Time("completed_at").Optional(),
		field.String("error_text").Optional(),
	}
}

func (UpstreamExchange) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("response_id", "started_at"),
		index.Fields("request_audit_id", "started_at"),
		index.Fields("trace_id"),
		index.Fields("upstream_id", "started_at"),
		index.Fields("status_code", "started_at"),
	}
}
