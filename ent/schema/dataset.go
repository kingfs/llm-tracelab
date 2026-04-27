package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Dataset struct {
	ent.Schema
}

func (Dataset) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "datasets"}}
}

func (Dataset) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		field.String("description").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now),
	}
}

func (Dataset) Indexes() []ent.Index {
	return []ent.Index{index.Fields("updated_at")}
}
