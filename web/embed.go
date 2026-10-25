// Package web embeds the built single-page application (npm run build writes
// it to dist/app).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// App returns the built application files, rooted at the directory that holds
// index.html. The file system is empty when the UI has not been built.
func App() fs.FS {
	sub, err := fs.Sub(dist, "dist/app")
	if err != nil {
		panic(err) // fs.Sub only fails for invalid paths
	}
	return sub
}
