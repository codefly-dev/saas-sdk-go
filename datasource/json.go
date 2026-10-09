package datasource

import (
	"bytes"
	"encoding/json"
	"errors"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

func inputStruct(input any) (*structpb.Struct, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return nil, &InputError{cause: err}
	}
	return jsonStruct(data)
}

func jsonStruct(data []byte) (*structpb.Struct, error) {
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, &InputError{cause: errors.New("datasource: input must be a JSON object")}
	}
	value := new(structpb.Struct)
	if err := protojson.Unmarshal(data, value); err != nil {
		return nil, &InputError{cause: err}
	}
	return value, nil
}

func structJSON(value *structpb.Struct) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	data, err := protojson.Marshal(value)
	return json.RawMessage(data), err
}
