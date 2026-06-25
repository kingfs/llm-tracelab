package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type TraceObservation struct {
	ent.Schema
}

func (TraceObservation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "trace_observations"}}
}

func (TraceObservation) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").StorageKey("trace_id").NotEmpty().Immutable(),
		field.String("parser").NotEmpty(),
		field.String("parser_version").NotEmpty(),
		field.String("status").NotEmpty(),
		field.String("provider").Default(""),
		field.String("operation").Default(""),
		field.String("model").Default(""),
		field.String("exchange_kind").Default(""),
		field.String("exchange_role").Default(""),
		field.String("parent_exchange_id").Default(""),
		field.Int("sequence_index").Default(0),
		field.String("request_audit_id").Default(""),
		field.String("response_id").Default(""),
		field.String("summary_json").Default("{}"),
		field.String("warnings_json").Default("[]"),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now),
	}
}

func (TraceObservation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "updated_at"),
		index.Fields("request_audit_id", "updated_at"),
		index.Fields("response_id", "updated_at"),
	}
}
