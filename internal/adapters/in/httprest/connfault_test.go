package httprest_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/adapters/in/httprest"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

// fakeExec injects a fixed pctx mutation so the handler executes a directive.
type fakeExec struct {
	mutate func(*pipeline.PipelineContext)
}

func (f fakeExec) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	f.mutate(pctx)
	return nil
}

func TestHang_BlocksThenClosesWithoutResponse(t *testing.T) {
	h := httprest.NewHandler(fakeExec{mutate: func(p *pipeline.PipelineContext) {
		p.ConnFault = "hang"
		p.ConnFaultMaxMs = 300
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	start := time.Now()
	resp, err := http.Get(srv.URL + "/x")
	elapsed := time.Since(start)
	// Server closes the connection after the hang window without writing a
	// response → client sees EOF / error, or an empty unparseable response.
	if err == nil {
		resp.Body.Close()
	}
	if elapsed < 280*time.Millisecond {
		t.Fatalf("hang returned too early: %v", elapsed)
	}
}

func TestReset_ClientSeesConnectionReset(t *testing.T) {
	h := httprest.NewHandler(fakeExec{mutate: func(p *pipeline.PipelineContext) {
		p.ConnFault = "reset"
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/x")
	// RST vs FIN timing differs per platform; the robust invariant is that the
	// client never observes a normal 200 with a full body.
	if err != nil {
		return // connection error (reset/EOF) — expected
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK && len(body) > 0 {
		t.Fatalf("expected reset, got normal 200 with %d-byte body", len(body))
	}
}

func TestHalfResponse_TruncatedBody(t *testing.T) {
	full := strings.Repeat("A", 1000)
	h := httprest.NewHandler(fakeExec{mutate: func(p *pipeline.PipelineContext) {
		p.ConnFault = "halfResponse"
		p.ConnFaultFraction = 0.5
		p.Response = &pipeline.MockResponse{Status: 200, Body: full}
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/x")
	if err != nil {
		// truncation may surface as a read error; acceptable
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if len(body) >= len(full) {
		t.Fatalf("expected truncated body, got %d bytes", len(body))
	}
}

func TestSlowBody_ThrottlesWrite(t *testing.T) {
	full := strings.Repeat("B", 4096)
	h := httprest.NewHandler(fakeExec{mutate: func(p *pipeline.PipelineContext) {
		p.Response = &pipeline.MockResponse{Status: 200, Body: full}
		p.SlowBodyBytesPerSec = 4096 // ~1s for 4096 bytes
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	start := time.Now()
	resp, err := http.Get(srv.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if time.Since(start) < 500*time.Millisecond {
		t.Fatalf("slowBody did not throttle: %v", time.Since(start))
	}
}
