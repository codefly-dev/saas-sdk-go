package workcontext_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	codefly "github.com/codefly-dev/sdk-go"

	"github.com/codefly-dev/saas-sdk-go/workcontext"
)

// echo is the handler under the middleware: it records its context and
// answers 200.
func echo(s *seen) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.record(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

// get performs one request against handler, attaching each token as a Work
// Context carrier the way a caller would (codefly.AttachWorkContext); more
// than one token exercises the duplicate-carrier rejection.
func get(t *testing.T, handler http.Handler, tokens ...string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/logins", nil)
	for _, token := range tokens {
		parsed, err := codefly.ParseWorkContextToken(token)
		if err != nil {
			t.Fatalf("ParseWorkContextToken: %v", err)
		}
		if len(req.Header.Values(codefly.WorkContextHeaderName)) == 0 {
			if err := codefly.AttachWorkContext(req, parsed); err != nil {
				t.Fatalf("AttachWorkContext: %v", err)
			}
		} else {
			req.Header.Add(codefly.WorkContextHeaderName, token)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, strings.TrimSpace(string(body))
}

func TestHTTPMiddlewareRequiresAVerifiedContext(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	valid := mintValid(t)
	cases := []struct {
		name   string
		tokens []string
		status int
		called bool
		ok     bool
	}{
		{"valid", []string{valid}, http.StatusOK, true, true},
		{"missing", nil, http.StatusUnauthorized, false, false},
		{"garbage", []string{"not.a-token"}, http.StatusUnauthorized, false, false},
		{"wrong audience", []string{mint(t, newSigner(t, signerOptions{}), "someone-else")}, http.StatusForbidden, false, false},
		{"two carriers", []string{valid, valid}, http.StatusUnauthorized, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &seen{}
			status, body := get(t, v.HTTPMiddleware(echo(s)), tc.tokens...)
			if status != tc.status {
				t.Errorf("status = %d (%s), want %d", status, body, tc.status)
			}
			s.expect(t, tc.called, tc.ok)
		})
	}
}

func TestHTTPMiddlewareOptionalLetsMissingThroughButNotInvalid(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys(), Optional: true})

	s := &seen{}
	if status, _ := get(t, v.HTTPMiddleware(echo(s))); status != http.StatusOK {
		t.Errorf("missing: status = %d, want 200", status)
	}
	s.expect(t, true, false)

	s = &seen{}
	if status, _ := get(t, v.HTTPMiddleware(echo(s)), "not.a-token"); status != http.StatusUnauthorized {
		t.Errorf("garbage: status = %d, want 401", status)
	}
	s.expect(t, false, false)

	s = &seen{}
	if status, _ := get(t, v.HTTPMiddleware(echo(s)), mint(t, newSigner(t, signerOptions{}), "someone-else")); status != http.StatusForbidden {
		t.Errorf("wrong audience: status = %d, want 403", status)
	}
	s.expect(t, false, false)

	s = &seen{}
	if status, _ := get(t, v.HTTPMiddleware(echo(s)), mintValid(t)); status != http.StatusOK {
		t.Errorf("valid: status = %d, want 200", status)
	}
	s.expect(t, true, true)
}

func TestRequireScopeHTTPGuardsARoute(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	valid := mintValid(t)

	s := &seen{}
	granted := v.HTTPMiddleware(workcontext.RequireScopeHTTP("lastlogin:logins", "read")(echo(s)))
	if status, body := get(t, granted, valid); status != http.StatusOK {
		t.Errorf("granted scope: status = %d (%s), want 200", status, body)
	}
	s.expect(t, true, true)

	s = &seen{}
	denied := v.HTTPMiddleware(workcontext.RequireScopeHTTP("lastlogin:reports", "read")(echo(s)))
	if status, body := get(t, denied, valid); status != http.StatusForbidden || !strings.Contains(body, "lastlogin:reports:read") {
		t.Errorf("restricted grant, kind-wide route: status = %d (%s), want 403 naming the scope", status, body)
	}
	s.expect(t, false, false)

	// Without the middleware in front there is nothing to authorize from.
	s = &seen{}
	bare := workcontext.RequireScopeHTTP("lastlogin:logins", "read")(echo(s))
	if status, _ := get(t, bare, valid); status != http.StatusUnauthorized {
		t.Errorf("no middleware: status = %d, want 401", status)
	}
	s.expect(t, false, false)
}
