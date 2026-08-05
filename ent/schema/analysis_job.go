package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AnalysisJob struct {
	ent.Schema
}

func (AnalysisJob) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "analysis_jobs"}}
}

func (AnalysisJob) Fields() []ent.Field {
	return []ent.Field{
		field.String("job_type").NotEmpty(),
		field.String("target_type").NotEmpty(),
		field.String("target_id").NotEmpty(),
		field.String("status").NotEmpty(),
		field.String("steps_json").Default("[]"),
		field.String("request_json").Default("{}"),
		field.String("result_json").Default("{}"),
		field.String("last_error").Default(""),
		field.Int("attempts").Default(0),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now),
		field.Time("started_at").Optional().Nillable(),
		field.Time("finished_at").Optional().Nillable(),
	}
}

func (AnalysisJob) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "updated_at"),
		index.Fields("target_type", "target_id", "created_at"),
	}
}
