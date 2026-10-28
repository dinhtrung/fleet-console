//go:build embedboard

// Package webui exposes the board embedded into this binary. It is populated
// only when built with the embedboard tag; `make build` copies dist/ here first.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var content embed.FS

// FS returns the embedded board root, or false when the build has no board.
func FS() (fs.FS, bool) {
	sub, err := fs.Sub(content, "dist")
	if err != nil {
		return nil, false
	}
	return sub, true
}
