package mcp

// EvalScript posts a script evaluation request to POST /api/script/eval.
// The handler (restapi.scriptEval) expects {"script": "<JS source>"}.
// The response is returned as a generic map (result, error, duration_ms).
func (c *Client) EvalScript(body any) (map[string]any, error) {
	var out map[string]any
	return out, c.post("/api/script/eval", body, &out)
}

// MetricsHistory fetches the rolling traffic time-series from GET /api/metrics/history.
// The response is {"rules": [...]}.
func (c *Client) MetricsHistory() (map[string]any, error) {
	var out map[string]any
	return out, c.get("/api/metrics/history", &out)
}
