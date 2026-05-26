package restapi

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"gopkg.in/yaml.v3"
)

//go:embed static
var staticFS embed.FS

//go:embed openapi.yaml
var openapiYAML []byte

var openapiJSON []byte

func init() {
	var v any
	if err := yaml.Unmarshal(openapiYAML, &v); err != nil {
		panic("restapi: openapi.yaml parse error: " + err.Error())
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic("restapi: openapi.yaml marshal error: " + err.Error())
	}
	openapiJSON = b
}

// serveUI registers a static file server at "/" on mux.
// API routes registered before this call take precedence.
func serveUI(mux *http.ServeMux) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("restapi: embed static subtree: " + err.Error())
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
}
