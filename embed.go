// Package pagueia embute o frontend compilado e a especificação OpenAPI no binário.
package pagueia

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var dist embed.FS

//go:embed api/openapi.yaml
var OpenAPI []byte

func Web() fs.FS {
	f, _ := fs.Sub(dist, "web/dist")
	return f
}
