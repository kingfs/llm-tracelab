-- create "api_tokens" table
CREATE TABLE `api_tokens` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `name` text NOT NULL, `token_hash` text NOT NULL, `prefix` text NOT NULL, `scope` text NOT NULL DEFAULT ('all'), `enabled` bool NOT NULL DEFAULT (true), `created_at` datetime NOT NULL, `expires_at` datetime NULL, `last_used_at` datetime NULL, `user_tokens` integer NOT NULL, CONSTRAINT `api_tokens_users_tokens` FOREIGN KEY (`user_tokens`) REFERENCES `users` (`id`) ON DELETE NO ACTION);
-- create index "api_tokens_token_hash_key" to table: "api_tokens"
CREATE UNIQUE INDEX `api_tokens_token_hash_key` ON `api_tokens` (`token_hash`);
-- create index "apitoken_prefix" to table: "api_tokens"
CREATE INDEX `apitoken_prefix` ON `api_tokens` (`prefix`);
-- create index "apitoken_enabled" to table: "api_tokens"
CREATE INDEX `apitoken_enabled` ON `api_tokens` (`enabled`);
-- create "users" table
CREATE TABLE `users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `username` text NOT NULL, `password_hash` text NOT NULL, `role` text NOT NULL DEFAULT ('admin'), `enabled` bool NOT NULL DEFAULT (true), `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `last_login_at` datetime NULL);
-- set sequence for "users" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("users", 4294967296);
-- create index "users_username_key" to table: "users"
CREATE UNIQUE INDEX `users_username_key` ON `users` (`username`);
