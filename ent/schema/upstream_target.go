package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type UpstreamTarget struct {
	ent.Schema
}

func (UpstreamTarget) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "upstream_targets"}}
}

func (UpstreamTarget) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("base_url").Default(""),
		field.String("provider_preset").Default(""),
		field.String("protocol_family").Default(""),
		field.String("routing_profile").Default(""),
		field.Bool("enabled").Default(true),
		field.Int("priority").Default(0),
		field.Float("weight").Default(0),
		field.Float("capacity_hint").Default(0),
		field.Time("last_refresh_at").Optional().Nillable(),
		field.String("last_refresh_status").Default(""),
		field.String("last_refresh_error").Default(""),
	}
}
