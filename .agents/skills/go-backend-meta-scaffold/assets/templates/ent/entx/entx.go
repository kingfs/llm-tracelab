package entx

import (
	"embed"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

//go:embed templates/pager.tmpl
var pager embed.FS

type Page struct {
	entc.DefaultExtension
}

func (*Page) Templates() []*gen.Template {
	return []*gen.Template{
		gen.MustParse(gen.NewTemplate("pager").
			ParseFS(pager, "templates/pager.tmpl")),
	}
}

