// observability/context.go
package observability

import (
	"context"
	"crypto/rand"
	"fmt"
)

// ctxKey is an unexported type for context keys in this package.
// Using a named int type prevents collisions with other packages.
type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyMethod
	keyPath
	keyProtocol
	keyTraceID
)

// RequestInfo holds request-scoped data stamped into context at adapter entry.
type RequestInfo struct {
	RequestID string
	Method    string
	Path      string
	Protocol  string
	TraceID   string
}

// StampRequest stamps request metadata into ctx and returns the new context.
// A random 8-byte hex request ID is generated automatically.
// Call this at the entry point of each protocol adapter (HTTP, gRPC, etc.).
func StampRequest(ctx context.Context, method, path, protocol string) context.Context {
	if ctx.Value(keyRequestID) != nil {
		return ctx // already stamped; don't overwrite the request ID
	}
	ctx = context.WithValue(ctx, keyRequestID, newRequestID())
	ctx = context.WithValue(ctx, keyMethod, method)
	ctx = context.WithValue(ctx, keyPath, path)
	ctx = context.WithValue(ctx, keyProtocol, protocol)
	return ctx
}

// StampTraceID adds a trace ID to ctx. Called by Tracer implementations
// after they generate or receive a trace ID from an upstream system.
func StampTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, keyTraceID, traceID)
}

// FromContext extracts all request-scoped info from ctx.
// Fields not present in ctx are returned as empty strings.
func FromContext(ctx context.Context) RequestInfo {
	ri := RequestInfo{}
	if v, ok := ctx.Value(keyRequestID).(string); ok {
		ri.RequestID = v
	}
	if v, ok := ctx.Value(keyMethod).(string); ok {
		ri.Method = v
	}
	if v, ok := ctx.Value(keyPath).(string); ok {
		ri.Path = v
	}
	if v, ok := ctx.Value(keyProtocol).(string); ok {
		ri.Protocol = v
	}
	if v, ok := ctx.Value(keyTraceID).(string); ok {
		ri.TraceID = v
	}
	return ri
}

// newRequestID generates a random 8-byte hex string for use as a request ID.
func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("observability: crypto/rand unavailable: " + err.Error())
	}
	return fmt.Sprintf("%x", b)
}
