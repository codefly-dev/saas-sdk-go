package settings_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func eq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func isTrue(t *testing.T, v bool, msg string) {
	t.Helper()
	if !v {
		t.Fatalf("expected true: %s", msg)
	}
}

func isFalse(t *testing.T, v bool, msg string) {
	t.Helper()
	if v {
		t.Fatalf("expected false: %s", msg)
	}
}

func isNil(t *testing.T, v any, msg string) {
	t.Helper()
	if !nilValue(v) {
		t.Fatalf("expected nil (%s): got %#v", msg, v)
	}
}

func notNil(t *testing.T, v any, msg string) {
	t.Helper()
	if nilValue(v) {
		t.Fatalf("expected non-nil: %s", msg)
	}
}

func nilValue(v any) bool {
	if v == nil {
		return true
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func errIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v", target, err)
	}
}

func errContains(t *testing.T, err error, substring string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substring) {
		t.Fatalf("expected error containing %q, got %v", substring, err)
	}
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	fn()
}

func jsonEq(t *testing.T, got, want string) {
	t.Helper()
	equal, err := jsonEqual(got, want)
	if err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !equal {
		t.Fatalf("JSON mismatch:\n got: %s\nwant: %s", got, want)
	}
}

// jsonEqual is the pure comparison behind jsonEq: it reports whether two JSON
// documents are semantically equal (object key order does not matter, array
// order does). Kept separate so the comparison itself is unit-tested rather
// than only exercised through assertions that fail open when wrong.
func jsonEqual(a, b string) (bool, error) {
	var aValue, bValue any
	if err := json.Unmarshal([]byte(a), &aValue); err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(b), &bValue); err != nil {
		return false, err
	}
	return reflect.DeepEqual(aValue, bValue), nil
}
