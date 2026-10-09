package datasource

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestInputJSONObject(t *testing.T) {
	for _, input := range []any{
		map[string]any{"nested": map[string]any{"items": []any{"one", nil, true}}},
		struct {
			Value string `json:"value"`
		}{Value: "example"},
		json.RawMessage(`{"count":2,"empty":{},"nothing":null}`),
	} {
		encoded, err := inputStruct(input)
		if err != nil {
			t.Fatal(err)
		}
		output, err := structJSON(encoded)
		if err != nil {
			t.Fatal(err)
		}
		original, _ := json.Marshal(input)
		var want, got any
		if err := json.Unmarshal(original, &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(output, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip = %s, want %s", output, original)
		}
	}
	for _, input := range []any{nil, "text", 42, []string{}, make(chan int),
		map[string]any{"bad": math.Inf(1)}, json.RawMessage(`{"broken"`)} {
		_, err := inputStruct(input)
		var invalid *InputError
		if !errors.As(err, &invalid) {
			t.Errorf("%T: error = %v, want InputError", input, err)
		}
	}
}
