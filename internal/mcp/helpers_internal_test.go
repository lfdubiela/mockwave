package mcp

import (
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

func reqWith(args map[string]any) mcpsdk.CallToolRequest {
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = args
	return req
}

func TestCaptureFilterArgs(t *testing.T) {
	q := captureFilterArgs(reqWith(map[string]any{
		"body":   map[string]any{"$.correlation_id": "abc"},
		"attr":   map[string]any{"tenant": "acme"},
		"query":  map[string]any{"source": "billing"},
		"method": "Publish",
		"limit":  float64(5),
	}))
	if got := q["body"]; len(got) != 1 || got[0] != "$.correlation_id:abc" {
		t.Fatalf("body = %v", q["body"])
	}
	if q.Get("attr") != "tenant:acme" {
		t.Fatalf("attr = %q", q.Get("attr"))
	}
	if q.Get("query") != "source:billing" {
		t.Fatalf("query = %q", q.Get("query"))
	}
	if q.Get("method") != "Publish" {
		t.Fatalf("method = %q", q.Get("method"))
	}
	if q.Get("limit") != "5" {
		t.Fatalf("limit = %q", q.Get("limit"))
	}
	if empty := captureFilterArgs(reqWith(map[string]any{})); len(empty) != 0 {
		t.Fatalf("empty args should yield no params, got %v", empty)
	}
}
