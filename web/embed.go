// Package web embeds the browser UI into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var content embed.FS

// UI returns the static files rooted at the UI's index.html.
func UI() fs.FS {
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
