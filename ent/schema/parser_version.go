package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ParserVersion struct {
	ent.Schema
}

func (ParserVersion) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "parser_versions"}}
}

func (ParserVersion) Fields() []ent.Field {
	return []ent.Field{
		field.String("parser").NotEmpty(),
		field.String("version").NotEmpty(),
		field.String("description").Default(""),
		field.Time("created_at").Default(time.Now),
	}
}

func (ParserVersion) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("parser", "version").Unique(),
	}
}
