package httprest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

type Executor interface {
	Execute(ctx context.Context, pctx *pipeline.PipelineContext) error
}

type Handler struct {
	pipeline Executor
}

func NewHandler(p Executor) *Handler {
	return &Handler{pipeline: p}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	headers := make(map[string]string)
	for k := range r.Header {
		headers[strings.ToLower(k)] = r.Header.Get(k)
	}
	query := make(map[string]string)
	for k := range r.URL.Query() {
		query[k] = r.URL.Query().Get(k)
	}
	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{
			Protocol: "http",
			Method:   r.Method,
			Path:     r.URL.Path,
			Headers:  headers,
			Query:    query,
			Body:     body,
			PathSegs: pathSegments(r.URL.Path),
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
	if resp.DelayMs > 0 {
		time.Sleep(time.Duration(resp.DelayMs) * time.Millisecond)
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if resp.Body != nil {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.Status)
	if resp.Body != nil {
		_ = json.NewEncoder(w).Encode(resp.Body)
	}
}

func pathSegments(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return []string{}
	}
	return parts
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
