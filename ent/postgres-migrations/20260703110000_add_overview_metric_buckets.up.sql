CREATE TABLE IF NOT EXISTS "overview_metric_buckets" (
	"bucket_start" timestamptz NOT NULL,
	"bucket_size_seconds" bigint NOT NULL,
	"request_count" bigint NOT NULL DEFAULT 0,
	"success_request" bigint NOT NULL DEFAULT 0,
	"failed_request" bigint NOT NULL DEFAULT 0,
	"total_tokens" bigint NOT NULL DEFAULT 0,
	"ttft_sum" bigint NOT NULL DEFAULT 0,
	"ttft_count" bigint NOT NULL DEFAULT 0,
	"duration_sum" bigint NOT NULL DEFAULT 0,
	"duration_count" bigint NOT NULL DEFAULT 0,
	"stream_count" bigint NOT NULL DEFAULT 0,
	"updated_at" timestamptz NOT NULL,
	PRIMARY KEY ("bucket_start", "bucket_size_seconds")
);

CREATE INDEX IF NOT EXISTS "idx_overview_metric_buckets_start" ON "overview_metric_buckets" ("bucket_start");

CREATE TABLE IF NOT EXISTS "overview_metric_bucket_members" (
	"path" character varying NOT NULL,
	"bucket_start" timestamptz NOT NULL,
	"bucket_size_seconds" bigint NOT NULL,
	"request_count" bigint NOT NULL DEFAULT 0,
	"success_request" bigint NOT NULL DEFAULT 0,
	"failed_request" bigint NOT NULL DEFAULT 0,
	"total_tokens" bigint NOT NULL DEFAULT 0,
	"ttft_sum" bigint NOT NULL DEFAULT 0,
	"ttft_count" bigint NOT NULL DEFAULT 0,
	"duration_sum" bigint NOT NULL DEFAULT 0,
	"duration_count" bigint NOT NULL DEFAULT 0,
	"stream_count" bigint NOT NULL DEFAULT 0,
	"updated_at" timestamptz NOT NULL,
	PRIMARY KEY ("path")
);

CREATE INDEX IF NOT EXISTS "idx_overview_metric_bucket_members_bucket" ON "overview_metric_bucket_members" ("bucket_start", "bucket_size_seconds");
