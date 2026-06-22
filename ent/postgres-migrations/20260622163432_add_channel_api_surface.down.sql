-- reverse: modify "channel_configs" table
ALTER TABLE "channel_configs" DROP COLUMN "capabilities_json", DROP COLUMN "mode", DROP COLUMN "api_type";
