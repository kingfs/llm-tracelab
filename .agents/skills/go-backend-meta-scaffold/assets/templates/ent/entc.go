//go:build ignore

package main

import (
	"log"

	"__MODULE_PATH__/ent/entx"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

func main() {
	if err := entc.Generate(
		"./schema",
		&gen.Config{
			Target:   "./dao",
			Package:  "__MODULE_PATH__/ent/dao",
			Features: gen.AllFeatures,
		},
		entc.Extensions(
			&entx.Page{},
		),
	); err != nil {
		log.Fatal("running ent codegen:", err)
	}
}

