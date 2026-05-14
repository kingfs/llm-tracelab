package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ChannelProbeRun struct {
	ent.Schema
}

func (ChannelProbeRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "channel_probe_runs"}}
}

func (ChannelProbeRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("channel_id").NotEmpty(),
		field.String("status").NotEmpty(),
		field.Time("started_at"),
		field.Time("completed_at").Optional().Nillable(),
		field.Int64("duration_ms").Default(0),
		field.Int("discovered_count").Default(0),
		field.Int("enabled_count").Default(0),
		field.String("endpoint").Default(""),
		field.Int("status_code").Default(0),
		field.String("error_text").Default(""),
		field.String("request_meta_json").Default("{}"),
		field.String("response_sample_json").Default("{}"),
	}
}

func (ChannelProbeRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("channel_id", "started_at"),
		index.Fields("status", "started_at"),
	}
}
