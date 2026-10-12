package moduleauthority

import (
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"testing"
)

type staticGateway struct {
	url    string
	client *http.Client
}

func (g staticGateway) BaseURL() string          { return g.url }
func (g staticGateway) HTTPClient() *http.Client { return g.client }

func TestNewFailsClosedOnAMissingSeam(t *testing.T) {
	valid := Seams{
		Gateway:       staticGateway{url: "https://gateway.example", client: http.DefaultClient},
		Authority:     Authority{Address: "accounts.example:9091", TLS: &tls.Config{MinVersion: tls.VersionTLS12}},
		InternalToken: testInternalToken,
	}
	if _, err := New(valid, moduleCredentials); err != nil {
		t.Fatalf("valid seams: %v", err)
	}
	for name, mutate := range map[string]func(*Seams){
		"no internal token":    func(s *Seams) { s.InternalToken = "" },
		"no gateway":           func(s *Seams) { s.Gateway = nil },
		"no gateway base URL":  func(s *Seams) { s.Gateway = staticGateway{client: http.DefaultClient} },
		"no gateway client":    func(s *Seams) { s.Gateway = staticGateway{url: "https://gateway.example"} },
		"no authority address": func(s *Seams) { s.Authority.Address = "" },
		"relative gateway URL": func(s *Seams) { s.Gateway = staticGateway{url: "gateway.example", client: http.DefaultClient} },
		"gateway userinfo": func(s *Seams) {
			s.Gateway = staticGateway{url: "https://u:p@gateway.example", client: http.DefaultClient}
		},
		"gateway query": func(s *Seams) {
			s.Gateway = staticGateway{url: "https://gateway.example?x=1", client: http.DefaultClient}
		},
		"gateway scheme":        func(s *Seams) { s.Gateway = staticGateway{url: "ftp://gateway.example", client: http.DefaultClient} },
		"authority with scheme": func(s *Seams) { s.Authority.Address = "https://accounts.example:9091" },
		"authority no port":     func(s *Seams) { s.Authority.Address = "accounts.example" },
	} {
		t.Run(name, func(t *testing.T) {
			seams := valid
			mutate(&seams)
			if _, err := New(seams, moduleCredentials); !errors.Is(err, ErrInvalidSeams) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidSeams)
			}
		})
	}
	for _, credentials := range []Credentials{{}, {Prefix: testPrefix}, {Token: staticToken(testProjectedToken)}} {
		if _, err := New(valid, credentials); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("credentials %+v: %v", credentials, err)
		}
	}
}

func TestPlaintextSeamsNeedLoopbackOrAnAssertion(t *testing.T) {
	seams := func(gateway, authority string, allow bool) Seams {
		return Seams{
			Gateway:           staticGateway{url: gateway, client: http.DefaultClient},
			Authority:         Authority{Address: authority},
			InternalToken:     testInternalToken,
			AllowInsecureHTTP: allow,
		}
	}
	for _, tc := range []struct {
		name      string
		gateway   string
		authority string
		allow     bool
		refused   string // the seam named in the refusal, or "" when admitted
	}{
		{"loopback IPv4", "http://127.0.0.1:8080", "127.0.0.1:9091", false, ""},
		{"loopback IPv6", "http://[::1]:8080", "[::1]:9091", false, ""},
		{"localhost", "http://localhost:8080", "localhost:9091", false, ""},
		{"in-cluster gateway without assertion", "http://auth-gateway.saas.svc:8080", "127.0.0.1:9091", false, "gateway"},
		{"in-cluster authority without assertion", "http://127.0.0.1:8080", "accounts.saas.svc:9091", false, "authority"},
		{"a name that merely looks local", "http://localhost.example:8080", "127.0.0.1:9091", false, "gateway"},
		{"in-cluster, asserted mesh-protected", "http://auth-gateway.saas.svc:8080", "accounts.saas.svc:9091", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(seams(tc.gateway, tc.authority, tc.allow), moduleCredentials)
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidSeams) || !strings.Contains(err.Error(), tc.refused) {
				t.Fatalf("error = %v, want the %s seam refused", err, tc.refused)
			}
		})
	}
	// TLS lifts the rule for the authority endpoint, https for the gateway.
	secure := seams("https://auth-gateway.saas.svc", "accounts.saas.svc:9091", false)
	secure.Authority.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	if _, err := New(secure, moduleCredentials); err != nil {
		t.Fatalf("TLS seams: %v", err)
	}
}

// accounts refuses any organization selector that is not a canonical UUID
// (InvalidArgument, an untyped 400 at the first work or operation mint); New
// refuses it before anything is sent, on the same rule — whichever mints the
// client would go on to make, source mints included, though they never send
// the field.
func TestNewRefusesANonCanonicalOrganization(t *testing.T) {
	const org = "6f1c3e2a-9b4d-4c8e-a1f0-2d3b4c5e6f7a"
	h := newHost(t)
	for _, org := range []string{
		"ACME",
		strings.ToUpper(org),
		"{" + org + "}",
		"urn:uuid:" + org,
		strings.ReplaceAll(org, "-", ""),
		" " + org,
	} {
		client, err := New(h.seams(), Credentials{Prefix: testPrefix, Token: staticToken(testProjectedToken), OrganizationID: org})
		if !errors.Is(err, ErrInvalidCredentials) || client != nil {
			t.Fatalf("organization %q: error = %v, want %v and no client", org, err, ErrInvalidCredentials)
		}
	}
	if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 0 {
		t.Fatalf("a refused construction reached the broker: %v", calls)
	}

	// The work and operation mints send a canonical organization.
	client := h.client(t, Credentials{Prefix: testPrefix, Token: staticToken(testProjectedToken), OrganizationID: org})
	if _, err := client.ModuleWorkContext(t.Context()); err != nil {
		t.Fatalf("canonical organization, work mint: %v", err)
	}
	if _, err := client.MintModuleOperationContext(t.Context(), "model"); err != nil {
		t.Fatalf("canonical organization, operation mint: %v", err)
	}
	// A source-only client leaves the selector empty, and sends none.
	source := h.client(t, moduleCredentials)
	if _, err := source.MintSourceOperationContext(t.Context(), exampleSource); err != nil {
		t.Fatalf("source-only client: %v", err)
	}
	_, _, bodies, _ := h.gateway.snapshot()
	if got := bodies[hostWorkContextPath]; len(got) != 1 || got[0]["organization_id"] != org {
		t.Fatalf("work mint bodies = %v", got)
	}
	if got := bodies[hostOperationContextPath]; len(got) != 1 || got[0]["organization_id"] != org {
		t.Fatalf("operation mint bodies = %v", got)
	}
	if got := bodies[hostSourceOperationContextPath]; len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("source mint bodies = %v, want prefix and source_id only", got)
	}
}
