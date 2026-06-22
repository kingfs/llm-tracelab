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
		field.String("summary_json").Default("{}"),
		field.String("warnings_json").Default("[]"),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now),
	}
}

func (TraceObservation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "updated_at"),
	}
}
