package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AnalysisRun struct {
	ent.Schema
}

func (AnalysisRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "analysis_runs"}}
}

func (AnalysisRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("trace_id").Default(""),
		field.String("session_id").Default(""),
		field.String("kind").NotEmpty(),
		field.String("analyzer").NotEmpty(),
		field.String("analyzer_version").NotEmpty(),
		field.String("model").Default(""),
		field.String("input_ref").Default(""),
		field.String("output_json").Default("{}"),
		field.String("status").NotEmpty(),
		field.Time("created_at").Default(time.Now),
	}
}

func (AnalysisRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("session_id", "kind", "created_at"),
		index.Fields("trace_id", "kind", "created_at"),
	}
}
