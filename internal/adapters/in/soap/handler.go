package soap

import (
	"context"
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

// Handler is an HTTP handler that accepts SOAP requests,
// normalizes them into NormalizedRequest{Protocol:"soap"}, and
// runs them through the pipeline.
type Handler struct {
	pipeline Executor
}

// NewHandler creates a SOAP handler backed by the given pipeline executor.
func NewHandler(p Executor) *Handler {
	return &Handler{pipeline: p}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	// SOAPAction header may be quoted: "http://example.com/GetUser" → strip quotes
	soapAction := strings.Trim(r.Header.Get("SOAPAction"), `"`)

	headers := make(map[string]string)
	for k := range r.Header {
		headers[strings.ToLower(k)] = r.Header.Get(k)
	}

	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{
			Protocol: "soap",
			Method:   "POST",
			Path:     soapAction,
			Headers:  headers,
			Body:     body,
		},
	}

	if err := h.pipeline.Execute(r.Context(), pctx); err != nil {
		writeFault(w, http.StatusNotFound, "Server", err.Error())
		return
	}
	if pctx.Response == nil {
		writeFault(w, http.StatusInternalServerError, "Server", "pipeline produced no response")
		return
	}

	resp := pctx.Response
	envelope, _ := resp.Body.(string)
	if envelope == "" {
		writeFault(w, http.StatusInternalServerError, "Server", "simulation has no soap_envelope")
		return
	}

	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(envelope))
}

func writeFault(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w,
		`<?xml version="1.0" encoding="UTF-8"?>`+
			`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">`+
			`<soap:Body><soap:Fault>`+
			`<faultcode>soap:%s</faultcode>`+
			`<faultstring>%s</faultstring>`+
			`</soap:Fault></soap:Body></soap:Envelope>`,
		code, escapeXML(message))
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
