package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type UpstreamModel struct {
	ent.Schema
}

func (UpstreamModel) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "upstream_models"}}
}

func (UpstreamModel) Fields() []ent.Field {
	return []ent.Field{
		field.String("upstream_id").NotEmpty(),
		field.String("model").NotEmpty(),
		field.String("source").Default(""),
		field.Time("seen_at").Default(time.Now),
	}
}

func (UpstreamModel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("upstream_id", "model").Unique(),
		index.Fields("model"),
	}
}
