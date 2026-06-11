package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mockwave/mockwave/internal/adapters/in/connfault"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

// Executor is the pipeline entry point.
type Executor interface {
	Execute(ctx context.Context, pctx *pipeline.PipelineContext) error
}

// Handler is an HTTP handler that accepts GraphQL POST requests,
// normalizes them into NormalizedRequest{Protocol:"graphql"}, and
// runs them through the pipeline.
type Handler struct {
	pipeline Executor
}

// NewHandler creates a GraphQL handler backed by the given pipeline executor.
func NewHandler(p Executor) *Handler {
	return &Handler{pipeline: p}
}

type gqlRequest struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	var gql gqlRequest
	if err := json.Unmarshal(body, &gql); err != nil {
		writeError(w, http.StatusBadRequest, "invalid graphql request body")
		return
	}

	headers := make(map[string]string)
	for k := range r.Header {
		headers[strings.ToLower(k)] = r.Header.Get(k)
	}

	opType := ExtractOperationType(gql.Query)
	opName := gql.OperationName
	if opName == "" {
		opName = ExtractOperationName(gql.Query)
	}

	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{
			Protocol: "graphql",
			Method:   opType,
			Path:     opName,
			Headers:  headers,
			Body:     body,
		},
	}

	if err := h.pipeline.Execute(r.Context(), pctx); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if pctx.Response == nil {
		writeError(w, http.StatusInternalServerError, "pipeline produced no response")
		return
	}

	resp := pctx.Response
	if d := resp.DelayMs + pctx.FaultDelayMs; d > 0 {
		time.Sleep(time.Duration(d) * time.Millisecond)
	}
	// Connection-level terminal faults bypass the normal response path.
	if pctx.ConnFault != "" {
		connfault.Handle(w, pctx)
		return
	}
	if pctx.FaultShortCircuit {
		writeFaultResponse(w, resp)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	if pctx.SlowBodyBytesPerSec > 0 {
		b := connfault.BodyBytes(map[string]interface{}{"data": resp.Body})
		connfault.ThrottledWrite(w, b, pctx.SlowBodyBytesPerSec)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data": resp.Body,
	})
}

// writeFaultResponse writes an injected chaos fault response directly,
// preserving the injected status, headers, and body instead of wrapping
// it in a GraphQL {"data": ...} envelope.
func writeFaultResponse(w http.ResponseWriter, resp *pipeline.MockResponse) {
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if resp.Body == nil {
		return
	}
	if s, ok := resp.Body.(string); ok {
		_, _ = w.Write([]byte(s))
		return
	}
	_ = json.NewEncoder(w).Encode(resp.Body)
}

// ExtractOperationType returns "query", "mutation", or "subscription".
// Exported for testing. Defaults to "query" for anonymous shorthand syntax.
func ExtractOperationType(query string) string {
	q := strings.TrimSpace(query)
	lower := strings.ToLower(q)
	for _, t := range []string{"mutation", "subscription", "query"} {
		if strings.HasPrefix(lower, t) {
			return t
		}
	}
	return "query" // anonymous shorthand { ... }
}

// ExtractOperationName parses the named operation from a query string.
// Returns "" for anonymous queries.
func ExtractOperationName(query string) string {
	q := strings.TrimSpace(query)
	lower := strings.ToLower(q)
	for _, prefix := range []string{"mutation ", "subscription ", "query "} {
		if strings.HasPrefix(lower, prefix) {
			rest := strings.TrimSpace(q[len(prefix):])
			// name ends at first space, '(', or '{'
			end := strings.IndexAny(rest, " ({")
			if end == -1 {
				return rest
			}
			return rest[:end]
		}
	}
	return ""
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"errors": []map[string]string{{"message": fmt.Sprintf("%s", msg)}},
	})
}
