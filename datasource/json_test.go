package datasource

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
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
		encoded, err := inputJSON(input)
		if err != nil {
			t.Fatal(err)
		}
		output, err := objectJSON(encoded)
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
		map[string]any{"bad": math.Inf(1)}, json.RawMessage(`{"broken"`), json.RawMessage(`{} {}`)} {
		_, err := inputJSON(input)
		var invalid *InputError
		if !errors.As(err, &invalid) {
			t.Errorf("%T: error = %v, want InputError", input, err)
		}
	}
}

func TestByteInputExplainsRawMessage(t *testing.T) {
	_, err := inputJSON([]byte(`{"count":2}`))
	var invalid *InputError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "json.RawMessage") {
		t.Fatalf("byte input = %v", err)
	}
}

func TestObjectJSONRejectsNonObjectAndTrailingData(t *testing.T) {
	for _, value := range []string{"", "null", "[]", "42", `"text"`, "{} {}", "{} trailing", "{", "{\"x\":\"\xff\"}"} {
		if _, err := objectJSON(value); err == nil {
			t.Errorf("accepted invalid object %q", value)
		}
	}
	const original = "  {\"id\":9007199254740993,\"decimal\":1.234567890123456789} \n"
	got, err := objectJSON(original)
	if err != nil || string(got) != original {
		t.Fatalf("JSON text changed: %q, %v", got, err)
	}
}
