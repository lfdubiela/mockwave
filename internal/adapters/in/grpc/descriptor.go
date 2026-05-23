package grpc

import (
	"fmt"
	"os"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// FileRegistry wraps a protobuf FileDescriptorSet loaded at startup.
type FileRegistry struct {
	files *protoregistry.Files
}

// LoadDescriptor reads a compiled FileDescriptorSet binary (.pb file produced by
// `protoc --descriptor_set_out=descriptor.pb --include_imports service.proto`).
func LoadDescriptor(pbFile string) (*FileRegistry, error) {
	data, err := os.ReadFile(pbFile)
	if err != nil {
		return nil, fmt.Errorf("grpc descriptor: read %q: %w", pbFile, err)
	}
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, fds); err != nil {
		return nil, fmt.Errorf("grpc descriptor: unmarshal: %w", err)
	}
	files, err := protodesc.NewFiles(fds)
	if err != nil {
		return nil, fmt.Errorf("grpc descriptor: build registry: %w", err)
	}
	return &FileRegistry{files: files}, nil
}

// FindResponseMessage returns the output MessageDescriptor for a full method path.
// Example fullMethod: "/users.UserService/GetUser"
func (r *FileRegistry) FindResponseMessage(fullMethod string) (protoreflect.MessageDescriptor, error) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash == -1 {
		return nil, fmt.Errorf("grpc descriptor: invalid method path %q", fullMethod)
	}
	serviceName := protoreflect.FullName(trimmed[:slash])
	methodName := protoreflect.Name(trimmed[slash+1:])

	desc, err := r.files.FindDescriptorByName(serviceName)
	if err != nil {
		return nil, fmt.Errorf("grpc descriptor: service %q not found", serviceName)
	}
	svc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("grpc descriptor: %q is not a service", serviceName)
	}
	method := svc.Methods().ByName(methodName)
	if method == nil {
		return nil, fmt.Errorf("grpc descriptor: method %q not found in %q", methodName, serviceName)
	}
	return method.Output(), nil
}

// JSONToProto converts a JSON string to proto-encoded bytes using the given MessageDescriptor.
func JSONToProto(msgJSON string, desc protoreflect.MessageDescriptor) ([]byte, error) {
	msg := dynamicpb.NewMessage(desc)
	if err := protojson.Unmarshal([]byte(msgJSON), msg); err != nil {
		return nil, fmt.Errorf("grpc: JSON→proto: %w", err)
	}
	return proto.Marshal(msg)
}
