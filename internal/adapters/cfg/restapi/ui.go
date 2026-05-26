package restapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

//go:embed openapi.yaml
var openapiYAML []byte

// serveUI registers a static file server at "/" on mux.
// API routes registered before this call take precedence.
func serveUI(mux *http.ServeMux) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("restapi: embed static subtree: " + err.Error())
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
}
