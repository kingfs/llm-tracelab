package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ExperimentRun struct {
	ent.Schema
}

func (ExperimentRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "experiment_runs"}}
}

func (ExperimentRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("name").Default(""),
		field.String("description").Default(""),
		field.String("baseline_eval_run_id").NotEmpty(),
		field.String("candidate_eval_run_id").NotEmpty(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Int("baseline_score_count").Default(0),
		field.Int("candidate_score_count").Default(0),
		field.Float("baseline_pass_rate").Default(0),
		field.Float("candidate_pass_rate").Default(0),
		field.Float("pass_rate_delta").Default(0),
		field.Int("matched_score_count").Default(0),
		field.Int("improvement_count").Default(0),
		field.Int("regression_count").Default(0),
	}
}

func (ExperimentRun) Indexes() []ent.Index {
	return []ent.Index{index.Fields("created_at", "id")}
}
