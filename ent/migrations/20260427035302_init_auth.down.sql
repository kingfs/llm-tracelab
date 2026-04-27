-- reverse: create index "users_username_key" to table: "users"
DROP INDEX `users_username_key`;
-- reverse: set sequence for "users" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "users";
-- reverse: create "users" table
DROP TABLE `users`;
-- reverse: create index "apitoken_enabled" to table: "api_tokens"
DROP INDEX `apitoken_enabled`;
-- reverse: create index "apitoken_prefix" to table: "api_tokens"
DROP INDEX `apitoken_prefix`;
-- reverse: create index "api_tokens_token_hash_key" to table: "api_tokens"
DROP INDEX `api_tokens_token_hash_key`;
-- reverse: create "api_tokens" table
DROP TABLE `api_tokens`;
