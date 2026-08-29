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
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("got is not valid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("want is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch:\n got: %s\nwant: %s", got, want)
	}
}
