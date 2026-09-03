package workcontext_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	codefly "github.com/codefly-dev/sdk-go"

	"github.com/codefly-dev/saas-sdk-go/workcontext"
)

func request(tokens ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/logins", nil)
	for _, token := range tokens {
		r.Header.Add(codefly.WorkContextHeaderName, token)
	}
	return r
}

func TestHTTPMiddleware(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	valid := mintValid(t)
	cases := []struct {
		name   string
		tokens []string
		status int
		called bool
	}{
		{"valid", []string{valid}, http.StatusOK, true},
		{"missing", nil, http.StatusUnauthorized, false},
		{"garbage", []string{"not.a-token"}, http.StatusUnauthorized, false},
		{"wrong audience", []string{mint(t, newSigner(t, signerOptions{}), "someone-else")}, http.StatusForbidden, false},
		{"two carriers", []string{valid, valid}, http.StatusUnauthorized, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &seen{}
			rec := httptest.NewRecorder()
			v.HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				s.record(r.Context())
			})).ServeHTTP(rec, request(tc.tokens...))
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			s.expect(t, tc.called, tc.called)
		})
	}
}

func TestHTTPMiddlewareOptional(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys(), Optional: true})

	s := &seen{}
	rec := httptest.NewRecorder()
	v.HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		s.record(r.Context())
	})).ServeHTTP(rec, request())
	if rec.Code != http.StatusOK {
		t.Errorf("missing under Optional: status = %d, want 200", rec.Code)
	}
	s.expect(t, true, false)

	// A present-but-invalid context is still rejected under Optional.
	s.reset()
	rec = httptest.NewRecorder()
	v.HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		s.record(r.Context())
	})).ServeHTTP(rec, request("not.a-token"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("garbage under Optional: status = %d, want 401", rec.Code)
	}
	s.expect(t, false, false)
}

func TestRequireScopeHTTP(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	guarded := func(kind, action string) http.Handler {
		return v.HTTPMiddleware(workcontext.RequireScopeHTTP(kind, action)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})))
	}
	cases := []struct {
		name   string
		kind   string
		action string
		token  string
		status int
	}{
		{"granted", "lastlogin:logins", "read", mintValid(t), http.StatusNoContent},
		{"denied", "lastlogin:reports", "read", mintValid(t), http.StatusForbidden},
		{"missing context", "lastlogin:logins", "read", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tokens []string
			if tc.token != "" {
				tokens = []string{tc.token}
			}
			rec := httptest.NewRecorder()
			guarded(tc.kind, tc.action).ServeHTTP(rec, request(tokens...))
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}
