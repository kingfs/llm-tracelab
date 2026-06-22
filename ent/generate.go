package ent

import "embed"

//go:generate go run entc.go

//go:embed migrations/*.sql
var Migrations embed.FS

//go:embed postgres-migrations/*.sql
var PostgresMigrations embed.FS
