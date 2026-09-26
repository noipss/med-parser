// Package web встраивает собранный React-интерфейс в бинарник.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS возвращает файлы интерфейса (frontend/dist, скопированные при сборке).
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
