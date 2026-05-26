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

	type evalResult struct {
		result map[string]interface{}
		err    error
	}

	resultCh := make(chan evalResult, 1)
	go func() {
		r, e := a.engine.Run(req.Script, syntheticReq, map[string]interface{}{})
		resultCh <- evalResult{r, e}
	}()

	var engineResult map[string]interface{}
	var engineErr error
	select {
	case res := <-resultCh:
		engineResult = res.result
		engineErr = res.err
	case <-time.After(500 * time.Millisecond):
		msg := "script execution timed out (500ms limit)"
		resp := evalResponse{Error: &msg, DurationMs: 500}
		writeJSON(w, 200, resp)
		return
	}

	elapsed := time.Since(start).Milliseconds()
	resp := evalResponse{DurationMs: elapsed}
	if engineErr != nil {
		msg := engineErr.Error()
		resp.Error = &msg
	} else {
		resp.Result = engineResult
	}
	writeJSON(w, 200, resp)
}
