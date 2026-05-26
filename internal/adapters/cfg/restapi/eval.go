package restapi

import (
	"encoding/json"
	"net/http"
	"time"
)

type evalRequest struct {
	Script string `json:"script"`
}

type evalResponse struct {
	Result     interface{} `json:"result"`
	Error      *string     `json:"error"`
	DurationMs int64       `json:"duration_ms"`
}

func (a *adminAPI) scriptEval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if a.engine == nil {
		writeError(w, 503, "script eval not configured")
		return
	}

	var req evalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return
	}

	syntheticReq := map[string]interface{}{
		"method":  "GET",
		"path":    "/test",
		"headers": map[string]interface{}{},
		"body":    nil,
	}

	start := time.Now()
	result, err := a.engine.Run(req.Script, syntheticReq, map[string]interface{}{})
	elapsed := time.Since(start).Milliseconds()

	resp := evalResponse{DurationMs: elapsed}
	if err != nil {
		msg := err.Error()
		resp.Error = &msg
	} else {
		resp.Result = result
	}
	writeJSON(w, 200, resp)
}
