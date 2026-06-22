package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ParseJob struct {
	ent.Schema
}

func (ParseJob) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "parse_jobs"}}
}

func (ParseJob) Fields() []ent.Field {
	return []ent.Field{
		field.String("trace_id").NotEmpty(),
		field.String("status").NotEmpty(),
		field.Int("attempts").Default(0),
		field.String("last_error").Default(""),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now),
	}
}

func (ParseJob) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "updated_at"),
	}
}
