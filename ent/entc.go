//go:build ignore

package main

import (
	"log"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

func main() {
	if err := entc.Generate(
		"./schema",
		&gen.Config{
			Target:   "./dao",
			Package:  "github.com/kingfs/llm-tracelab/ent/dao",
			Features: gen.AllFeatures,
		},
	); err != nil {
		log.Fatal("running ent codegen:", err)
	}
}
