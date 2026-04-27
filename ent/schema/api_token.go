package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type APIToken struct {
	ent.Schema
}

func (APIToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("token_hash").NotEmpty().Unique().Sensitive(),
		field.String("prefix").NotEmpty(),
		field.String("scope").Default("all"),
		field.Bool("enabled").Default(true),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("expires_at").Optional().Nillable(),
		field.Time("last_used_at").Optional().Nillable(),
	}
}

func (APIToken) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("tokens").Unique().Required(),
	}
}

func (APIToken) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("prefix"),
		index.Fields("enabled"),
	}
}
