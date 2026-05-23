package grpc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcadapter "github.com/mockwave/mockwave/internal/adapters/in/grpc"
)

// buildTestDescriptorFile creates a minimal FileDescriptorSet .pb file in a temp
// directory and returns its path. The descriptor declares:
//
//	package test;
//	service TestService { rpc GetItem (ItemRequest) returns (ItemResponse); }
//	message ItemRequest  { string id = 1; }
//	message ItemResponse { string id = 1; string name = 2; }
func buildTestDescriptorFile(t *testing.T) string {
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
	path := filepath.Join(dir, "test.pb")
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

func TestLoadDescriptor_Success(t *testing.T) {
	pbFile := buildTestDescriptorFile(t)

	reg, err := grpcadapter.LoadDescriptor(pbFile)
	require.NoError(t, err)
	assert.NotNil(t, reg)
}

func TestLoadDescriptor_MissingFile(t *testing.T) {
	_, err := grpcadapter.LoadDescriptor("/nonexistent/path/test.pb")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "grpc descriptor: read")
}

func TestLoadDescriptor_InvalidData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pb")
	require.NoError(t, os.WriteFile(path, []byte("not protobuf"), 0o644))

	_, err := grpcadapter.LoadDescriptor(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "grpc descriptor:")
}

func TestFindResponseMessage_Success(t *testing.T) {
	pbFile := buildTestDescriptorFile(t)
	reg, err := grpcadapter.LoadDescriptor(pbFile)
	require.NoError(t, err)

	desc, err := reg.FindResponseMessage("/test.TestService/GetItem")
	require.NoError(t, err)
	assert.Equal(t, "ItemResponse", string(desc.Name()))
}

func TestFindResponseMessage_InvalidPath(t *testing.T) {
	pbFile := buildTestDescriptorFile(t)
	reg, err := grpcadapter.LoadDescriptor(pbFile)
	require.NoError(t, err)

	_, err = reg.FindResponseMessage("noslash")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid method path")
}

func TestFindResponseMessage_UnknownService(t *testing.T) {
	pbFile := buildTestDescriptorFile(t)
	reg, err := grpcadapter.LoadDescriptor(pbFile)
	require.NoError(t, err)

	_, err = reg.FindResponseMessage("/unknown.Service/Method")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFindResponseMessage_UnknownMethod(t *testing.T) {
	pbFile := buildTestDescriptorFile(t)
	reg, err := grpcadapter.LoadDescriptor(pbFile)
	require.NoError(t, err)

	_, err = reg.FindResponseMessage("/test.TestService/NoSuchMethod")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestJSONToProto_Success(t *testing.T) {
	pbFile := buildTestDescriptorFile(t)
	reg, err := grpcadapter.LoadDescriptor(pbFile)
	require.NoError(t, err)

	desc, err := reg.FindResponseMessage("/test.TestService/GetItem")
	require.NoError(t, err)

	data, err := grpcadapter.JSONToProto(`{"id":"42","name":"hello"}`, desc)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestJSONToProto_InvalidJSON(t *testing.T) {
	pbFile := buildTestDescriptorFile(t)
	reg, err := grpcadapter.LoadDescriptor(pbFile)
	require.NoError(t, err)

	desc, err := reg.FindResponseMessage("/test.TestService/GetItem")
	require.NoError(t, err)

	_, err = grpcadapter.JSONToProto(`not json`, desc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JSON→proto")
}
