package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type EvalRun struct {
	ent.Schema
}

func (EvalRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "eval_runs"}}
}

func (EvalRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("dataset_id").Default(""),
		field.String("source_type").Default(""),
		field.String("source_id").Default(""),
		field.String("evaluator_set").NotEmpty(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("completed_at").Default(time.Now),
		field.Int("trace_count").Default(0),
		field.Int("score_count").Default(0),
		field.Int("pass_count").Default(0),
		field.Int("fail_count").Default(0),
	}
}

func (EvalRun) Indexes() []ent.Index {
	return []ent.Index{index.Fields("created_at")}
}
