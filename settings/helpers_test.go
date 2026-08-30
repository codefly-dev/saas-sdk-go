package settings_test

import (
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"
)

// These tests guard the assertion helpers themselves: a helper that silently
// returns the wrong verdict would make every test using it pass when it should
// fail, so the fail-open-prone predicates are covered directly.

func TestNilValueDistinguishesNilFromNonNil(t *testing.T) {
	var nilPointer *descriptorpb.FileOptions
	nonNilPointer := &descriptorpb.FileOptions{}
	var nilInterface any

	cases := map[string]struct {
		value any
		want  bool
	}{
		"untyped nil":         {nil, true},
		"typed nil pointer":   {nilPointer, true},
		"nil interface":       {nilInterface, true},
		"nil slice":           {[]byte(nil), true},
		"non-nil pointer":     {nonNilPointer, false},
		"non-empty string":    {"value", false},
		"zero int":            {0, false},
		"false bool":          {false, false},
		"empty non-nil slice": {[]byte{}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := nilValue(tc.value); got != tc.want {
				t.Fatalf("nilValue(%#v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestJSONEqualComparesSemantically(t *testing.T) {
	equalCases := [][2]string{
		{`{"a":1,"b":2}`, `{"b":2,"a":1}`}, // key order ignored
		{`{"a": 1}`, `{"a":1}`},            // whitespace ignored
		{`[1,2,3]`, `[1,2,3]`},
	}
	for _, c := range equalCases {
		equal, err := jsonEqual(c[0], c[1])
		if err != nil {
			t.Fatalf("unexpected error comparing %s and %s: %v", c[0], c[1], err)
		}
		if !equal {
			t.Fatalf("expected %s == %s", c[0], c[1])
		}
	}

	unequalCases := [][2]string{
		{`{"a":1}`, `{"a":2}`}, // different values
		{`[1,2,3]`, `[3,2,1]`}, // array order matters
		{`{"a":1}`, `{"a":1,"b":2}`},
	}
	for _, c := range unequalCases {
		equal, err := jsonEqual(c[0], c[1])
		if err != nil {
			t.Fatalf("unexpected error comparing %s and %s: %v", c[0], c[1], err)
		}
		if equal {
			t.Fatalf("expected %s != %s", c[0], c[1])
		}
	}

	if _, err := jsonEqual(`{`, `{}`); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
