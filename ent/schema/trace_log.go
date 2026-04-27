package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type TraceLog struct {
	ent.Schema
}

func (TraceLog) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "logs"}}
}

func (TraceLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").StorageKey("path").NotEmpty().Immutable(),
		field.String("trace_id").NotEmpty().Unique(),
		field.Int64("mod_time_ns"),
		field.Int64("file_size"),
		field.String("version").NotEmpty(),
		field.String("request_id").Default(""),
		field.Time("recorded_at").Default(time.Now),
		field.String("model").Default(""),
		field.String("provider").Default(""),
		field.String("operation").Default(""),
		field.String("endpoint").Default(""),
		field.String("url").Default(""),
		field.String("method").Default(""),
		field.Int("status_code").Default(0),
		field.Int64("duration_ms").Default(0),
		field.Int64("ttft_ms").Default(0),
		field.String("client_ip").Default(""),
		field.Int64("content_length").Default(0),
		field.String("error_text").Default(""),
		field.Int("prompt_tokens").Default(0),
		field.Int("completion_tokens").Default(0),
		field.Int("total_tokens").Default(0),
		field.Int("cached_tokens").Default(0),
		field.Int64("req_header_len").Default(0),
		field.Int64("req_body_len").Default(0),
		field.Int64("res_header_len").Default(0),
		field.Int64("res_body_len").Default(0),
		field.Bool("is_stream").Default(false),
		field.String("session_id").Default(""),
		field.String("session_source").Default(""),
		field.String("window_id").Default(""),
		field.String("client_request_id").Default(""),
		field.String("selected_upstream_id").Default(""),
		field.String("selected_upstream_base_url").Default(""),
		field.String("selected_upstream_provider_preset").Default(""),
		field.String("routing_policy").Default(""),
		field.Float("routing_score").Default(0),
		field.Int("routing_candidate_count").Default(0),
		field.String("routing_failure_reason").Default(""),
	}
}

func (TraceLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("recorded_at"),
		index.Fields("model", "recorded_at"),
		index.Fields("session_id", "recorded_at"),
		index.Fields("request_id"),
	}
}
