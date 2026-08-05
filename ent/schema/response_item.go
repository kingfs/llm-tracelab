package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ResponseItem stores a versioned Responses input/output/tool item payload.
type ResponseItem struct {
	ent.Schema
}

func (ResponseItem) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "response_items"}}
}

func (ResponseItem) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.Enum("kind").Values("input", "output", "tool_call", "tool_result", "reasoning", "summary"),
		field.String("response_id").Default(""),
		field.String("conversation_id").Default(""),
		field.JSON("payload", map[string]any{}),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (ResponseItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("conversation_id", "created_at"),
		index.Fields("response_id", "kind"),
	}
}
