// Package web holds the built Vue frontend (npm run build writes web/dist).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist is the built frontend, rooted at its index.html.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
