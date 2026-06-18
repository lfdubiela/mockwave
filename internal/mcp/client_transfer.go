package mcp

import "github.com/mockwave/mockwave/domain"

// ExportConfig fetches the full config from GET /api/export.
// The response is the domain.Config struct (rules, simulations, fault_profiles,
// scenarios, event_rules).
func (c *Client) ExportConfig() (*domain.Config, error) {
	var out domain.Config
	return &out, c.get("/api/export", &out)
}

// ImportConfig bulk-imports a config payload.
// When preview is true the request goes to POST /api/import/preview (dry-run:
// returns conflict info, makes no writes). Otherwise it goes to POST /api/import.
// The response is returned as a generic map so callers can pass it through as-is.
func (c *Client) ImportConfig(body any, preview bool) (map[string]any, error) {
	path := "/api/import"
	if preview {
		path = "/api/import/preview"
	}
	var out map[string]any
	return out, c.post(path, body, &out)
}
