CREATE TABLE IF NOT EXISTS "model_aliases" (
	"id" character varying NOT NULL,
	"alias" character varying NOT NULL,
	"target_model" character varying NOT NULL,
	"channel_id" character varying NOT NULL DEFAULT '',
	"enabled" boolean NOT NULL DEFAULT true,
	"description" character varying NOT NULL DEFAULT '',
	"source" character varying NOT NULL DEFAULT 'manual',
	"created_at" timestamptz NOT NULL,
	"updated_at" timestamptz NOT NULL,
	PRIMARY KEY ("id")
);

CREATE INDEX IF NOT EXISTS "idx_model_aliases_alias" ON "model_aliases" ("alias");
CREATE INDEX IF NOT EXISTS "idx_model_aliases_target" ON "model_aliases" ("target_model");
CREATE INDEX IF NOT EXISTS "idx_model_aliases_channel" ON "model_aliases" ("channel_id");
