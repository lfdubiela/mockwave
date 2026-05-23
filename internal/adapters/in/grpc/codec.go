package grpc

import (
	"encoding/json"
	"fmt"
)

// RawCodec overrides the default "proto" codec so that the gRPC framework
// passes raw []byte through without attempting protobuf (de)serialization.
// This allows the mock server to accept any gRPC message without .proto files.
type RawCodec struct{}

func (RawCodec) Name() string { return "proto" }

func (RawCodec) Marshal(v interface{}) ([]byte, error) {
	switch val := v.(type) {
	case []byte:
		return val, nil
	case *[]byte:
		if val == nil {
			return nil, nil
		}
		return *val, nil
	default:
		return json.Marshal(v)
	}
}

func (RawCodec) Unmarshal(data []byte, v interface{}) error {
	switch val := v.(type) {
	case *[]byte:
		if val == nil {
			return fmt.Errorf("grpc codec: nil target pointer")
		}
		*val = append([]byte(nil), data...)
		return nil
	default:
		return json.Unmarshal(data, v)
	}
}
