// Package web embeds the dashboard and API docs pages so they ship inside the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var static embed.FS

// Static is the dashboard's file tree, rooted at the static directory.
var Static = mustSub(static, "static")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
