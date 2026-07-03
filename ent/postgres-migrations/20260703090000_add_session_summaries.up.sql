CREATE TABLE IF NOT EXISTS "session_summaries" (
	"session_id" character varying NOT NULL,
	"session_source" character varying NOT NULL DEFAULT '',
	"request_count" bigint NOT NULL DEFAULT 0,
	"first_seen" timestamptz NOT NULL,
	"last_seen" timestamptz NOT NULL,
	"last_model" character varying NOT NULL DEFAULT '',
	"providers" character varying NOT NULL DEFAULT '',
	"success_request" bigint NOT NULL DEFAULT 0,
	"failed_request" bigint NOT NULL DEFAULT 0,
	"success_rate" double precision NOT NULL DEFAULT 0,
	"total_tokens" bigint NOT NULL DEFAULT 0,
	"avg_ttft" double precision NOT NULL DEFAULT 0,
	"total_duration" bigint NOT NULL DEFAULT 0,
	"stream_count" bigint NOT NULL DEFAULT 0,
	"updated_at" timestamptz NOT NULL,
	PRIMARY KEY ("session_id")
);

CREATE INDEX IF NOT EXISTS "session_summaries_last_seen" ON "session_summaries" ("last_seen" DESC, "session_id" DESC);
CREATE INDEX IF NOT EXISTS "session_summaries_last_model" ON "session_summaries" ("last_model");
