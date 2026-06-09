package restapi

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

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

// serveUI registers the admin UI on mux. The index page gets a data-theme
// attribute injected from the mw-theme cookie so the correct theme paints
// on first load. All other static assets are served verbatim.
func serveUI(mux *http.ServeMux) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("restapi: embed static subtree: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	indexHTML, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic("restapi: read index.html: " + err.Error())
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			fileServer.ServeHTTP(w, r)
			return
		}
		theme := themeFromRequest(r)
		out := injectTheme(indexHTML, theme)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(out)
	})
}

var allowedThemes = map[string]bool{"macos": true, "amber": true}

func themeFromRequest(r *http.Request) string {
	if c, err := r.Cookie("mw-theme"); err == nil && allowedThemes[c.Value] {
		return c.Value
	}
	return "macos"
}

func injectTheme(html []byte, theme string) []byte {
	return []byte(strings.Replace(
		string(html),
		`<html lang="en">`,
		`<html lang="en" data-theme="`+theme+`">`,
		1,
	))
}
