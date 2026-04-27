package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type DatasetExample struct {
	ent.Schema
}

func (DatasetExample) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "dataset_examples"}}
}

func (DatasetExample) Fields() []ent.Field {
	return []ent.Field{
		field.String("dataset_id").NotEmpty(),
		field.String("trace_id").NotEmpty(),
		field.Int("position").Default(0),
		field.Time("added_at").Default(time.Now),
		field.String("source_type").Default(""),
		field.String("source_id").Default(""),
		field.String("note").Default(""),
	}
}

func (DatasetExample) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("dataset_id", "trace_id").Unique(),
		index.Fields("dataset_id", "position"),
	}
}
