package grpc

import (
	"context"
	"strings"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

func init() {
	// Override the default "proto" codec globally so all gRPC servers in this
	// process use raw byte pass-through instead of protobuf serialization.
	encoding.RegisterCodec(RawCodec{})
}

// Executor is the pipeline entry point.
type Executor interface {
	Execute(ctx context.Context, pctx *pipeline.PipelineContext) error
}

// Handler is a generic gRPC server handler. It accepts any service/method call
// using grpc.UnknownServiceHandler, normalizes it into NormalizedRequest{Protocol:"grpc"},
// and runs it through the pipeline.
type Handler struct {
	pipeline Executor
	registry *FileRegistry // may be nil — proto conversion skipped when nil
}

// NewHandler creates a gRPC handler backed by the given pipeline executor.
// registry may be nil; if provided, it is used to convert GRPCMessage JSON to proto bytes.
func NewHandler(p Executor, registry *FileRegistry) *Handler {
	return &Handler{pipeline: p, registry: registry}
}

// NewGRPCServer returns a configured *grpc.Server using UnknownServiceHandler.
func (h *Handler) NewGRPCServer() *googlegrpc.Server {
	return googlegrpc.NewServer(
		googlegrpc.UnknownServiceHandler(h.handleUnknown),
	)
}

func (h *Handler) handleUnknown(_ interface{}, stream googlegrpc.ServerStream) error {
	fullMethod, _ := googlegrpc.Method(stream.Context())

	var rawBody []byte
	if err := stream.RecvMsg(&rawBody); err != nil {
		return status.Errorf(codes.Internal, "recv message: %v", err)
	}

	headers := make(map[string]string)
	if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
		for k, vals := range md {
			if len(vals) > 0 && !strings.HasPrefix(k, ":") {
				headers[k] = vals[0]
			}
		}
	}

	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{
			Protocol: "grpc",
			Method:   fullMethod,
			Path:     fullMethod,
			Headers:  headers,
			Body:     rawBody,
		},
	}

	if err := h.pipeline.Execute(stream.Context(), pctx); err != nil {
		return status.Errorf(codes.NotFound, "%v", err)
	}
	if pctx.Response == nil {
		return status.Errorf(codes.Internal, "pipeline produced no response")
	}

	resp := pctx.Response
	grpcCode := codes.Code(resp.Status)
	if grpcCode != codes.OK {
		return status.Errorf(grpcCode, "simulated gRPC error")
	}

	responseBytes := h.buildResponseBytes(fullMethod, resp.Body)
	return stream.SendMsg(responseBytes)
}

// buildResponseBytes converts the simulation's response body to wire bytes.
// If a FileRegistry is configured, converts JSON → proto bytes.
// Otherwise, sends the JSON string as raw bytes.
func (h *Handler) buildResponseBytes(fullMethod string, body interface{}) []byte {
	msgJSON, ok := body.(string)
	if !ok || msgJSON == "" {
		return nil
	}
	if h.registry != nil {
		desc, err := h.registry.FindResponseMessage(fullMethod)
		if err == nil {
			if protoBytes, err := JSONToProto(msgJSON, desc); err == nil {
				return protoBytes
			}
		}
	}
	return []byte(msgJSON)
}
