package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data": resp.Body,
	})
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
