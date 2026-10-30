// Package web embeds the built React UI so the server ships as one binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the contents of web/dist. The directory always exists because
// dist/placeholder.txt is committed; a build without `npm run build` serves the
// "UI not built" page from internal/ui instead of failing to compile.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // only possible if the embed directive changes
	}
	return sub
}
