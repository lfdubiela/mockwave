package soap

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
	// Connection-level terminal faults bypass the normal response path. The
	// FaultStage leaves Response nil for hang/reset faults, so this must run
	// before the nil-Response guard below.
	if pctx.ConnFault != "" {
		connfault.Handle(w, pctx)
		return
	}
	if pctx.Response == nil {
		writeFault(w, http.StatusInternalServerError, "Server", "pipeline produced no response")
		return
	}

	resp := pctx.Response
	if d := resp.DelayMs + pctx.FaultDelayMs; d > 0 {
		time.Sleep(time.Duration(d) * time.Millisecond)
	}

	if pctx.FaultShortCircuit {
		writeFaultResponse(w, resp)
		return
	}

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
	if pctx.SlowBodyBytesPerSec > 0 {
		connfault.ThrottledWrite(w, []byte(envelope), pctx.SlowBodyBytesPerSec)
		return
	}
	_, _ = w.Write([]byte(envelope))
}

// writeFaultResponse writes an injected chaos fault response directly,
// preserving the injected status instead of demanding a SOAP envelope.
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
