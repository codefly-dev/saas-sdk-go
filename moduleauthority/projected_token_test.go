package moduleauthority

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEveryMintRejectsInvalidProjectedTokensBeforeDispatch(t *testing.T) {
	for _, mint := range brokerMints(t) {
		for _, tc := range []struct {
			name   string
			source ProjectedTokenSource
		}{
			{"empty", staticToken("")},
			{"whitespace", staticToken(" \t\r\n")},
			{"oversized", staticToken(strings.Repeat("x", 64*1024+1))},
			{"unreadable", failingToken{errors.New("projection unavailable")}},
		} {
			t.Run(mint.path+"/"+tc.name, func(t *testing.T) {
				h := newHost(t)
				client := h.client(t, Credentials{Prefix: testPrefix, Token: tc.source})
				if err := mint.call(client); !errors.Is(err, ErrInvalidCredentials) {
					t.Fatalf("error = %v, want ErrInvalidCredentials", err)
				}
				if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 0 {
					t.Fatalf("invalid token reached the broker: %v", calls)
				}
			})
		}
	}
}

func TestEveryMintAcceptsTheHostTokenSizeBoundary(t *testing.T) {
	for _, mint := range brokerMints(t) {
		t.Run(mint.path, func(t *testing.T) {
			h := newHost(t)
			token := strings.Repeat("x", 64*1024)
			h.gateway.projectedToken = token
			client := h.client(t, Credentials{Prefix: testPrefix, Token: staticToken(" \t" + token + "\n")})
			if err := mint.call(client); err != nil {
				t.Fatal(err)
			}
			_, _, _, headers := h.gateway.snapshot()
			assertProjectedBearer(t, headers[0], token)
		})
	}
}

// Model a kubelet rotation with a replaced file, after client construction
// and between actual mints. Module Work Contexts inside the refresh window
// must reread the projection too; a cached Work Context itself needs no mint.
func TestEveryMintReadsTheRotatedProjectedFile(t *testing.T) {
	for _, mint := range brokerMints(t) {
		t.Run(mint.path, func(t *testing.T) {
			h := newHost(t)
			h.gateway.expires = time.Now().Add(20 * time.Second)
			path := filepath.Join(t.TempDir(), "projected-token")
			client := h.client(t, Credentials{Prefix: testPrefix, Token: ProjectedTokenFile(path)})
			for _, token := range []string{"first-projection", "rotated-projection"} {
				if err := os.WriteFile(path+".next", []byte(token+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path+".next", path); err != nil {
					t.Fatal(err)
				}
				h.gateway.mu.Lock()
				h.gateway.projectedToken = token
				h.gateway.mu.Unlock()
				if err := mint.call(client); err != nil {
					t.Fatal(err)
				}
			}
			calls, _, _, headers := h.gateway.snapshot()
			if len(calls) != 2 || calls[0] != mint.path || calls[1] != mint.path {
				t.Fatalf("broker calls = %v, want two mints", calls)
			}
			assertProjectedBearer(t, headers[0], "first-projection")
			assertProjectedBearer(t, headers[1], "rotated-projection")
		})
	}
}

func assertProjectedBearer(t *testing.T, header http.Header, token string) {
	t.Helper()
	if !reflect.DeepEqual(header.Values("Authorization"), []string{"Bearer " + token}) {
		t.Fatal("mint must send exactly one Authorization containing the current projected bearer")
	}
	if len(header.Values("X-Codefly-Module-Secret")) != 0 {
		t.Fatal("mint sent the removed module-secret header")
	}
}

func TestMintBodiesMatchTaggedGateway(t *testing.T) {
	for _, org := range []string{"", testTenant} {
		for _, mint := range brokerMints(t) {
			t.Run(mint.path+"/org="+org, func(t *testing.T) {
				h := newHost(t)
				client := h.client(t, Credentials{Prefix: testPrefix, Token: staticToken(testProjectedToken), OrganizationID: org})
				if err := mint.call(client); err != nil {
					t.Fatal(err)
				}
				want := map[string]string{"prefix": testPrefix}
				switch mint.path {
				case hostWorkContextPath:
					if org != "" {
						want["organization_id"] = org
					}
				case hostOperationContextPath:
					want["binding"] = "binding"
					if org != "" {
						want["organization_id"] = org
					}
				case hostSourceOperationContextPath:
					want["source_id"] = exampleSource
				}
				_, _, bodies, headers := h.gateway.snapshot()
				if got := bodies[mint.path]; len(got) != 1 || !reflect.DeepEqual(got[0], want) {
					t.Fatalf("body = %v, want exactly %v", got, want)
				}
				assertProjectedBearer(t, headers[0], testProjectedToken)
			})
		}
	}
}

func TestHostFakeRefusesRemovedOrMalformedCredentials(t *testing.T) {
	for _, mint := range brokerMints(t) {
		for _, tc := range []struct {
			name          string
			authorization []string
			legacy        bool
			acceptedToken string
		}{
			{name: "missing"},
			{name: "duplicate", authorization: []string{"Bearer " + testProjectedToken, "Bearer " + testProjectedToken}},
			{name: "non bearer", authorization: []string{"Basic " + testProjectedToken}},
			{name: "wrong case", authorization: []string{"bearer " + testProjectedToken}},
			{name: "empty", authorization: []string{"Bearer "}},
			{name: "whitespace", authorization: []string{"Bearer  \t"}},
			{name: "oversized", authorization: []string{"Bearer " + strings.Repeat("x", 64*1024+1)}, acceptedToken: strings.Repeat("x", 64*1024+1)},
			{name: "secret only", legacy: true},
			{name: "dual header", authorization: []string{"Bearer " + testProjectedToken}, legacy: true, acceptedToken: testProjectedToken},
		} {
			t.Run(mint.path+"/"+tc.name, func(t *testing.T) {
				g := newFakeGateway(t)
				g.projectedToken = tc.acceptedToken
				request := httptest.NewRequest(http.MethodPost, mint.path, strings.NewReader(`{"prefix":"documents","binding":"binding","source_id":"`+exampleSource+`"}`))
				request.Header.Set("X-Codefly-Internal-Token", testInternalToken)
				for _, value := range tc.authorization {
					request.Header.Add("Authorization", value)
				}
				if tc.legacy {
					request.Header.Set("X-Codefly-Module-Secret", "removed-credential")
				}
				response := httptest.NewRecorder()
				g.serve(response, request)
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", response.Code)
				}
				_, _, bodies, _ := g.snapshot()
				if len(bodies) != 0 {
					t.Fatal("malformed credential passed gateway authentication and reached body decoding")
				}
			})
		}
	}
}

func TestProjectedTokenFileBoundsItsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	for _, size := range []int{64 * 1024, 64*1024 + 1} {
		if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o600); err != nil {
			t.Fatal(err)
		}
		token, err := ProjectedTokenFile(path).ProjectedToken()
		if size == 64*1024 {
			if err != nil || len(token) != size {
				t.Fatalf("bounded token: length %d, error %v", len(token), err)
			}
		} else if err == nil || token != "" {
			t.Fatal("oversized file must not yield a token")
		}
	}
}
