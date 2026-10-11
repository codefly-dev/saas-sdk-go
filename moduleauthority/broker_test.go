package moduleauthority

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

func TestTheGatewayFakeRefusesTheConnectPathTheOldClientUsed(t *testing.T) {
	// The defect this package was rewritten for: a Connect call to the
	// gateway for an EXPOSURE_INTERNAL procedure. The fake must answer it the
	// way the real gateway does, or every other test here proves nothing.
	h := newHost(t)
	old := accountsv1connect.NewModuleCapabilitiesServiceClient(h.gateway.HTTPClient(), h.gateway.BaseURL())
	_, err := old.MintModuleWorkContext(t.Context(), connect.NewRequest(&ModuleMintWorkContextRequest{Prefix: testPrefix, ServiceAccountToken: testProjectedToken}))
	if connect.CodeOf(err) != connect.CodeUnimplemented { // Connect's mapping of HTTP 404
		t.Fatalf("a Connect call at the gateway: %v, want 404 (unimplemented)", err)
	}
	if _, stray, _, _ := h.gateway.snapshot(); len(stray) != 1 || stray[0] != accountsv1connect.ModuleCapabilitiesServiceMintModuleWorkContextProcedure {
		t.Fatalf("stray paths = %v", stray)
	}
}

func TestModuleWorkContextIsMintedAtTheBrokerAndCached(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)

	for range 3 {
		module, err := client.ModuleWorkContext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if module.Encoded() != "module.token1" {
			t.Fatalf("module Work Context = %q", module.Encoded())
		}
	}
	calls, _, bodies, headers := h.gateway.snapshot()
	if len(calls) != 1 || calls[0] != hostWorkContextPath {
		t.Fatalf("broker calls = %v, want one %s", calls, hostWorkContextPath)
	}
	if body := bodies[hostWorkContextPath][0]; len(body) != 1 || body["prefix"] != testPrefix {
		t.Fatalf("body = %v, want only the prefix: the projected token travels as the bearer", body)
	}
	header := headers[0]
	if header.Get("X-Codefly-Internal-Token") != testInternalToken || header.Get("Authorization") != "Bearer "+testProjectedToken ||
		header.Get("X-Codefly-Module-Secret") != "" || header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", header)
	}
	h.assertNoStrayPaths(t)
}

func TestModuleWorkContextIsRefreshedBeforeExpiry(t *testing.T) {
	h := newHost(t)
	h.gateway.expires = time.Now().Add(20 * time.Second) // inside the 30s skew
	client := h.client(t, moduleCredentials)

	first, err := client.ModuleWorkContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.ModuleWorkContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.Encoded() == second.Encoded() {
		t.Fatalf("a capability inside the refresh window was reused: %q", first.Encoded())
	}
}

func TestMintModuleOperationContextSendsOnlyTheBindingAndNeverCaches(t *testing.T) {
	h := newHost(t)
	h.gateway.expires = time.Now().Add(time.Minute)
	client := h.client(t, moduleCredentials)

	for range 2 {
		issued, err := client.MintModuleOperationContext(t.Context(), "model")
		if err != nil {
			t.Fatal(err)
		}
		if issued.Token.Encoded() != "operation.token" || issued.Audience != "modelservice" || issued.BindingID != "model" ||
			issued.Tenant != testTenant || issued.PrincipalID != testPrincipal {
			t.Fatalf("unexpected operation context: %+v", issued)
		}
		if want := h.gateway.expires.Truncate(time.Second); !issued.ExpiresAt.Equal(want) {
			t.Fatalf("expires at = %v, want %v", issued.ExpiresAt, want)
		}
	}
	calls, _, bodies, headers := h.gateway.snapshot()
	if len(calls) != 2 {
		t.Fatalf("broker calls = %v, want 2: an operation context is never cached", calls)
	}
	body := bodies[hostOperationContextPath][1]
	if len(body) != 2 || body["prefix"] != testPrefix || body["binding"] != "model" {
		t.Fatalf("body = %v", body)
	}
	if headers[1].Get("Authorization") != "Bearer "+testProjectedToken || headers[1].Get("X-Codefly-Internal-Token") != testInternalToken {
		t.Fatalf("headers = %v", headers[1])
	}
	h.assertNoStrayPaths(t)
}

func TestMintModuleOperationContextTypesTheBrokerStatuses(t *testing.T) {
	for _, tc := range []struct {
		refuse refusal
		want   error
	}{
		{refusal{http.StatusUnauthorized, "unauthorized"}, ErrInvalidCredentials},
		{refusal{http.StatusForbidden, "forbidden"}, ErrPermissionDenied},
		{refusal{http.StatusServiceUnavailable, "operation context unavailable"}, ErrExecutionUnestablished},
	} {
		h := newHost(t)
		h.gateway.refuse[hostOperationContextPath] = tc.refuse
		_, err := h.client(t, moduleCredentials).MintModuleOperationContext(t.Context(), "interactive")
		if !errors.Is(err, tc.want) {
			t.Fatalf("HTTP %d: %v, want %v", tc.refuse.status, err, tc.want)
		}
		var be *BrokerError
		if !errors.As(err, &be) || be.StatusCode != tc.refuse.status {
			t.Fatalf("HTTP %d: broker error = %+v", tc.refuse.status, be)
		}
		assertBrokerError(t, err, tc.refuse.status)
		if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 1 || calls[0] != hostOperationContextPath {
			t.Fatalf("broker calls = %v, want only the operation-context mint", calls)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusBadGateway} {
		h := newHost(t)
		h.gateway.refuse[hostOperationContextPath] = refusal{status, "operation context unavailable"}
		_, err := h.client(t, moduleCredentials).MintModuleOperationContext(t.Context(), "model")
		var be *BrokerError
		if !errors.As(err, &be) || be.StatusCode != status || errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("HTTP %d: %v", status, err)
		}
	}
}

func TestTheBrokerRefusesAWrongPerimeterCredential(t *testing.T) {
	h := newHost(t)
	seams := h.seams()
	seams.InternalToken = "not-the-perimeter-token"
	client, err := New(seams, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ModuleWorkContext(t.Context()); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong internal token: %v", err)
	}
	wrongToken := h.client(t, Credentials{Prefix: testPrefix, Token: staticToken("another-pods-token")})
	if _, err := wrongToken.ModuleWorkContext(t.Context()); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("a token the host refuses: %v", err)
	}
}

// The token is read from its source before EVERY mint, never once at New: the
// kubelet rotates the projection under a running process, and the copy read
// at boot expires long before the process does.
func TestTheProjectedTokenIsReadBeforeEveryMint(t *testing.T) {
	h := newHost(t)
	reads := 0
	source := countingToken{token: testProjectedToken, reads: &reads}
	client := h.client(t, Credentials{Prefix: testPrefix, Token: source})
	if reads != 0 {
		t.Fatalf("New read the token %d times; it must read none", reads)
	}
	for range 2 {
		if _, err := client.MintModuleOperationContext(t.Context(), "model"); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 2 {
		t.Fatalf("token reads = %d, want one per mint", reads)
	}
}

// A token source that cannot answer mints nothing and contacts nobody.
func TestAnUnreadableProjectedTokenContactsNoBroker(t *testing.T) {
	h := newHost(t)
	client := h.client(t, Credentials{Prefix: testPrefix, Token: failingToken{errors.New("projection not mounted")}})
	if _, err := client.ModuleWorkContext(t.Context()); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unreadable token: %v", err)
	}
	if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 0 {
		t.Fatalf("broker calls = %v, want none", calls)
	}
}

type countingToken struct {
	token string
	reads *int
}

func (c countingToken) ProjectedToken() (string, error) {
	*c.reads++
	return c.token, nil
}

func TestMintModuleOperationContextRejectsAnInvalidCapability(t *testing.T) {
	for name, setup := range map[string]func(*fakeGateway){
		"expired":         func(g *fakeGateway) { g.expires = time.Now().Add(-time.Minute) },
		"another binding": func(g *fakeGateway) { g.binding = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			setup(h.gateway)
			if _, err := h.client(t, moduleCredentials).MintModuleOperationContext(t.Context(), "model"); !errors.Is(err, ErrInvalidCapability) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidCapability)
			}
		})
	}
}

func TestMintsRejectIncompleteInputWithoutNetwork(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	if _, err := client.MintModuleOperationContext(t.Context(), ""); !errors.Is(err, ErrInvalidExchange) {
		t.Fatalf("no binding: %v", err)
	}
	if _, err := client.MintSourceOperationContext(t.Context(), ""); !errors.Is(err, ErrInvalidExchange) {
		t.Fatalf("no source: %v", err)
	}
	if _, err := client.ExchangeOperation(t.Context(), ExchangeRequest{}); !errors.Is(err, ErrInvalidExchange) {
		t.Fatalf("no binding or parent: %v", err)
	}
	if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 0 {
		t.Fatalf("incomplete input reached the broker: %v", calls)
	}
	if calls := h.authority.snapshot(); len(calls) != 0 {
		t.Fatalf("incomplete input reached the authority endpoint: %v", calls)
	}
}

func TestTheBrokerClientNeverFollowsARedirect(t *testing.T) {
	// A redirect would replay the internal token and the module secret to an
	// address the transport rule never judged.
	h := newHost(t)
	target := newFakeGateway(t)
	h.gateway.refuse[hostWorkContextPath] = refusal{http.StatusTemporaryRedirect, target.BaseURL() + hostWorkContextPath}
	client := h.client(t, moduleCredentials)
	_, err := client.ModuleWorkContext(t.Context())
	var be *BrokerError
	if !errors.As(err, &be) || be.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("redirect: %v", err)
	}
	if calls, _, _, _ := target.snapshot(); len(calls) != 0 {
		t.Fatalf("the redirect was followed: %v", calls)
	}
}

func TestProjectedTokenFileReadsTheProjectionAsItIsNow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  first-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := ProjectedTokenFile(path)
	if token, err := source.ProjectedToken(); err != nil || token != "first-token" {
		t.Fatalf("token = %q, %v", token, err)
	}
	// The kubelet rewrites the file under the running process.
	if err := os.WriteFile(path, []byte("rotated-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if token, err := source.ProjectedToken(); err != nil || token != "rotated-token" {
		t.Fatalf("after rotation: token = %q, %v", token, err)
	}
	// Empty mid-rotation, or never mounted, is an error and never a token.
	if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ProjectedToken(); err == nil {
		t.Fatal("an empty projection must not read as a token")
	}
	for _, missing := range []ProjectedTokenFile{"", ProjectedTokenFile(filepath.Join(t.TempDir(), "absent"))} {
		if _, err := missing.ProjectedToken(); err == nil {
			t.Fatalf("%q: an absent projection must not read as a token", missing)
		}
	}
}
