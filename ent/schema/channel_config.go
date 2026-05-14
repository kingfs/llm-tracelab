package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ChannelConfig struct {
	ent.Schema
}

func (ChannelConfig) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "channel_configs"}}
}

func (ChannelConfig) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		field.String("description").Default(""),
		field.String("source").Default("manual"),
		field.String("base_url").NotEmpty(),
		field.String("provider_preset").Default(""),
		field.String("protocol_family").Default(""),
		field.String("routing_profile").Default(""),
		field.String("api_version").Default(""),
		field.String("deployment").Default(""),
		field.String("project").Default(""),
		field.String("location").Default(""),
		field.String("model_resource").Default(""),
		field.Bytes("api_key_ciphertext").Optional().Nillable(),
		field.String("api_key_hint").Default(""),
		field.String("headers_json").Default("{}"),
		field.Bool("enabled").Default(true),
		field.Int("priority").Default(0),
		field.Float("weight").Default(1),
		field.Float("capacity_hint").Default(1),
		field.String("model_discovery").Default("list_models"),
		field.Bool("allow_unknown_models").Default(false),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now),
		field.Time("last_probe_at").Optional().Nillable(),
		field.String("last_probe_status").Default(""),
		field.String("last_probe_error").Default(""),
	}
}

func (ChannelConfig) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("enabled", "priority"),
		index.Fields("provider_preset"),
	}
}
