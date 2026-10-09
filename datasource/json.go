package datasource

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// Payloads remain JSON text, preserving integer precision and number spelling.
// Only operation declaration schemas cross the Struct boundary below.
func inputJSON(input any) (string, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return "", &InputError{cause: err}
	}
	if _, err := objectJSON(string(data)); err != nil {
		return "", &InputError{cause: err}
	}
	return string(data), nil
}

func objectJSON(value string) (json.RawMessage, error) {
	data := []byte(value)
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' || !utf8.Valid(data) || !json.Valid(data) {
		return nil, errors.New("datasource: expected exactly one JSON object")
	}
	return json.RawMessage(data), nil
}

func schemaStruct(data []byte) (*structpb.Struct, error) {
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, &InputError{cause: errors.New("datasource: input must be a JSON object")}
	}
	value := new(structpb.Struct)
	if err := protojson.Unmarshal(data, value); err != nil {
		return nil, &InputError{cause: err}
	}
	return value, nil
}

func schemaJSON(value *structpb.Struct) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	data, err := protojson.Marshal(value)
	return json.RawMessage(data), err
}
