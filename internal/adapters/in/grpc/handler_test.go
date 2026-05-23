package grpc_test

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	grpcadapter "github.com/mockwave/mockwave/internal/adapters/in/grpc"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

type mockExec struct {
	fn func(ctx context.Context, pctx *pipeline.PipelineContext) error
}

func (m *mockExec) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	return m.fn(ctx, pctx)
}

func TestGRPCHandler_SimulationResponse(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			assert.Equal(t, "grpc", pctx.Request.Protocol)
			assert.Equal(t, "/test.TestService/GetItem", pctx.Request.Method)
			pctx.Response = &pipeline.MockResponse{
				Status: 0, // codes.OK
				Body:   `{"id":"42","name":"mock"}`,
			}
			return nil
		},
	}

	h := grpcadapter.NewHandler(exec, nil)
	grpcSrv := h.NewGRPCServer()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	codec := grpcadapter.RawCodec{}
	conn, err := googlegrpc.NewClient(lis.Addr().String(),
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
		googlegrpc.WithDefaultCallOptions(googlegrpc.ForceCodec(codec)),
	)
	require.NoError(t, err)
	defer conn.Close()

	var response []byte
	err = conn.Invoke(context.Background(), "/test.TestService/GetItem", []byte(`{}`), &response)
	require.NoError(t, err)
	assert.Equal(t, `{"id":"42","name":"mock"}`, string(response))
}

func TestGRPCHandler_NoMatch(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			return fmt.Errorf("no rule matched")
		},
	}

	h := grpcadapter.NewHandler(exec, nil)
	grpcSrv := h.NewGRPCServer()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	codec := grpcadapter.RawCodec{}
	conn, err := googlegrpc.NewClient(lis.Addr().String(),
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
		googlegrpc.WithDefaultCallOptions(googlegrpc.ForceCodec(codec)),
	)
	require.NoError(t, err)
	defer conn.Close()

	var response []byte
	err = conn.Invoke(context.Background(), "/test.TestService/Unknown", []byte(`{}`), &response)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NotFound")
}

func TestRawCodec_MarshalUnmarshal(t *testing.T) {
	codec := grpcadapter.RawCodec{}
	assert.Equal(t, "proto", codec.Name())

	input := []byte(`{"id":"1"}`)
	marshaled, err := codec.Marshal(input)
	require.NoError(t, err)
	assert.Equal(t, input, marshaled)

	var output []byte
	require.NoError(t, codec.Unmarshal(marshaled, &output))
	assert.Equal(t, input, output)
}
