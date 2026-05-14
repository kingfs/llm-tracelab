package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ChannelModel struct {
	ent.Schema
}

func (ChannelModel) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "channel_models"}}
}

func (ChannelModel) Fields() []ent.Field {
	return []ent.Field{
		field.String("channel_id").NotEmpty(),
		field.String("model").NotEmpty(),
		field.String("display_name").Default(""),
		field.String("source").Default(""),
		field.Bool("enabled").Default(true),
		field.Int("supports_responses").Optional().Nillable(),
		field.Int("supports_chat_completions").Optional().Nillable(),
		field.Int("supports_embeddings").Optional().Nillable(),
		field.Int("context_window").Optional().Nillable(),
		field.String("input_modalities_json").Default("[]"),
		field.String("output_modalities_json").Default("[]"),
		field.String("raw_model_json").Default("{}"),
		field.Time("first_seen_at").Default(time.Now),
		field.Time("last_seen_at").Default(time.Now),
		field.Time("last_probe_at").Optional().Nillable(),
	}
}

func (ChannelModel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("channel_id", "model").Unique(),
		index.Fields("model"),
		index.Fields("channel_id", "enabled"),
	}
}
