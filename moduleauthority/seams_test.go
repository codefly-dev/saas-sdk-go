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
	for _, credentials := range []Credentials{{}, {Prefix: testPrefix}, {Secret: testSecret}} {
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
