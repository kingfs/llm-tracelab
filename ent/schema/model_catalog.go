package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type ModelCatalog struct {
	ent.Schema
}

func (ModelCatalog) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "model_catalog"}}
}

func (ModelCatalog) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").StorageKey("model").NotEmpty().Immutable(),
		field.String("display_name").Default(""),
		field.String("family").Default(""),
		field.String("vendor").Default(""),
		field.String("description").Default(""),
		field.String("tags_json").Default("[]"),
		field.Time("first_seen_at").Default(time.Now),
		field.Time("last_seen_at").Default(time.Now),
		field.Time("last_used_at").Optional().Nillable(),
	}
}
