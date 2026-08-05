-- modify "channel_configs" table
ALTER TABLE "channel_configs" ADD COLUMN "api_type" character varying NOT NULL DEFAULT '', ADD COLUMN "mode" character varying NOT NULL DEFAULT '', ADD COLUMN "capabilities_json" character varying NOT NULL DEFAULT '{}';
