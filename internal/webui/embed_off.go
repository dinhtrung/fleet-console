//go:build !embedboard

// Package webui exposes the board embedded into this binary. A build without
// the embedboard tag serves the API only.
package webui

import "io/fs"

// FS returns false when the build has no board.
func FS() (fs.FS, bool) { return nil, false }
