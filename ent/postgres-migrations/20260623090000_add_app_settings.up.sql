CREATE TABLE IF NOT EXISTS "app_settings" (
	"setting_key" character varying NOT NULL,
	"value_json" character varying NOT NULL,
	"updated_at" timestamptz NOT NULL,
	PRIMARY KEY ("setting_key")
);
