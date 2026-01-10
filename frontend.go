package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
)

//go:embed all:web/build
var embeddedFrontend embed.FS

func newFrontendHandler() (http.Handler, error) {
	frontendFS, err := fs.Sub(embeddedFrontend, "web/build")
	if err != nil {
		return nil, fmt.Errorf("embedded frontend not found (build the web UI first): %w", err)
	}

	if _, err := frontendFS.Open("index.html"); err != nil {
		return nil, fmt.Errorf("embedded frontend is missing index.html (run `npm run build` in ./web): %w", err)
	}

	fileServer := http.FileServer(http.FS(frontendFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fileServer.ServeHTTP(w, r)
	}), nil
}
