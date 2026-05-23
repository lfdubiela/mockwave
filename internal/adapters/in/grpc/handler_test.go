package grpc_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcadapter "github.com/mockwave/mockwave/internal/adapters/in/grpc"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

type mockExec struct {
	fn func(ctx context.Context, pctx *pipeline.PipelineContext) error
}

func (m *mockExec) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	return m.fn(ctx, pctx)
}

// buildDescriptorFile creates a minimal in-memory FileDescriptorSet and writes it
// to a temp file, returning the file path. The descriptor defines:
//
//	package test; service TestService { rpc GetItem(...) returns (ItemResponse); }
//	message ItemResponse { string id=1; string name=2; }
func buildDescriptorFile(t *testing.T) string {
	t.Helper()

	strType := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	labelOpt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	num := func(n int32) *int32 { return &n }

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test.proto"),
		Package: proto.String("test"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("ItemRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("id"), Number: num(1), Type: strType, Label: labelOpt},
				},
			},
			{
				Name: proto.String("ItemResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("id"), Number: num(1), Type: strType, Label: labelOpt},
					{Name: proto.String("name"), Number: num(2), Type: strType, Label: labelOpt},
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("TestService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("GetItem"),
						InputType:  proto.String(".test.ItemRequest"),
						OutputType: proto.String(".test.ItemResponse"),
					},
				},
			},
		},
	}

	fds := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}}
	data, err := proto.Marshal(fds)
	require.NoError(t, err)

	dir := t.TempDir()
	pbPath := filepath.Join(dir, "test.pb")
	require.NoError(t, os.WriteFile(pbPath, data, 0o644))
	return pbPath
}

func loadRegistry(t *testing.T, pbPath string) *grpcadapter.FileRegistry {
	t.Helper()
	reg, err := grpcadapter.LoadDescriptor(pbPath)
	require.NoError(t, err)
	return reg
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

func TestRawCodec_Marshal_PtrBytes(t *testing.T) {
	codec := grpcadapter.RawCodec{}

	// Marshal *[]byte (non-nil)
	input := []byte(`{"x":1}`)
	out, err := codec.Marshal(&input)
	require.NoError(t, err)
	assert.Equal(t, input, out)

	// Marshal nil *[]byte
	var nilPtr *[]byte
	out2, err2 := codec.Marshal(nilPtr)
	require.NoError(t, err2)
	assert.Nil(t, out2)
}

func TestRawCodec_Marshal_JSONFallback(t *testing.T) {
	codec := grpcadapter.RawCodec{}
	type point struct {
		X int `json:"x"`
	}
	out, err := codec.Marshal(point{X: 7})
	require.NoError(t, err)
	assert.Equal(t, `{"x":7}`, string(out))
}

func TestRawCodec_Unmarshal_JSONFallback(t *testing.T) {
	codec := grpcadapter.RawCodec{}
	type point struct {
		X int `json:"x"`
	}
	var p point
	require.NoError(t, codec.Unmarshal([]byte(`{"x":3}`), &p))
	assert.Equal(t, 3, p.X)
}

func TestGRPCHandler_WithRegistry(t *testing.T) {
	pbPath := buildDescriptorFile(t)
	registry := loadRegistry(t, pbPath)

	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.Response = &pipeline.MockResponse{
				Status: 0,
				Body:   `{"id":"99","name":"test"}`,
			}
			return nil
		},
	}

	h := grpcadapter.NewHandler(exec, registry)
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
	assert.NotEmpty(t, response)
}

func TestGRPCHandler_NonOKStatus(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.Response = &pipeline.MockResponse{
				Status: 5, // codes.NotFound
				Body:   "",
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
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NotFound")
}
