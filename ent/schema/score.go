package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Score struct {
	ent.Schema
}

func (Score) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "scores"}}
}

func (Score) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("trace_id").NotEmpty(),
		field.String("session_id").Default(""),
		field.String("dataset_id").Default(""),
		field.String("eval_run_id").Default(""),
		field.String("evaluator_key").NotEmpty(),
		field.Float("value").Default(0),
		field.String("status").Default(""),
		field.String("label").Default(""),
		field.String("explanation").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (Score) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trace_id", "created_at"),
		index.Fields("session_id", "created_at"),
		index.Fields("dataset_id", "created_at"),
		index.Fields("eval_run_id", "created_at"),
	}
}
