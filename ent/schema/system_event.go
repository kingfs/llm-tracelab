package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SystemEvent struct {
	ent.Schema
}

func (SystemEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "system_events"}}
}

func (SystemEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("fingerprint").NotEmpty().Unique(),
		field.String("source").NotEmpty(),
		field.String("category").NotEmpty(),
		field.String("severity").NotEmpty(),
		field.String("status").NotEmpty(),
		field.String("title").Default(""),
		field.String("message").Default(""),
		field.String("details_json").Default("{}"),
		field.String("trace_id").Default(""),
		field.String("session_id").Default(""),
		field.String("job_id").Default(""),
		field.String("upstream_id").Default(""),
		field.String("model").Default(""),
		field.Int("occurrence_count").Default(1),
		field.Time("first_seen_at").Default(time.Now),
		field.Time("last_seen_at").Default(time.Now),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now),
		field.Time("read_at").Optional().Nillable(),
		field.Time("resolved_at").Optional().Nillable(),
	}
}

func (SystemEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "last_seen_at"),
		index.Fields("source", "category", "last_seen_at"),
		index.Fields("trace_id", "last_seen_at"),
	}
}
