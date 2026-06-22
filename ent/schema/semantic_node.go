package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SemanticNode struct {
	ent.Schema
}

func (SemanticNode) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "semantic_nodes"}}
}

func (SemanticNode) Fields() []ent.Field {
	return []ent.Field{
		field.String("trace_id").NotEmpty(),
		field.String("node_id").NotEmpty(),
		field.String("parent_node_id").Default(""),
		field.String("provider_type").Default(""),
		field.String("normalized_type").Default(""),
		field.String("role").Default(""),
		field.String("path").Default(""),
		field.Int("node_index").Default(0),
		field.Int("depth").Default(0),
		field.String("text_preview").Default(""),
		field.String("json").Default(""),
		field.String("raw").Default(""),
		field.String("raw_ref").Default(""),
		field.Time("created_at").Default(time.Now),
	}
}

func (SemanticNode) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trace_id", "node_id").Unique(),
		index.Fields("trace_id", "depth", "node_index"),
	}
}
