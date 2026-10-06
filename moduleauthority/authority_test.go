package moduleauthority

import (
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc/credentials"
)

func TestExchangeOperationCallsTheAuthorityEndpointAsTheModule(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	request := ExchangeRequest{BindingID: "binding-1", Parent: token(t, "parent.token"), Lookup: true}

	for range 2 {
		issued, err := client.ExchangeOperation(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if issued.Encoded() != "child.token" {
			t.Fatalf("issued token = %q", issued.Encoded())
		}
	}

	calls := h.authority.snapshot()
	if len(calls) != 2 {
		t.Fatalf("authority calls = %d, want 2", len(calls))
	}
	for _, call := range calls {
		if call.method != "/saas.accounts.v1.ModuleCapabilitiesService/ExchangeDelegatedOperationAudience" {
			t.Fatalf("method = %q", call.method)
		}
		if len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken {
			t.Fatalf("x-codefly-internal-token = %v", call.internalToken)
		}
		if len(call.workContext) != 1 || call.workContext[0] != "module.token1" {
			t.Fatalf("x-codefly-work-context = %v", call.workContext)
		}
		if call.request.GetBindingId() != "binding-1" || call.request.GetParentWorkContextToken() != "parent.token" || !call.request.GetLookup() {
			t.Fatalf("request = %+v", call.request)
		}
	}
	// One mint at the broker, cached across both exchanges; nothing else.
	if brokerCalls, _, _, _ := h.gateway.snapshot(); len(brokerCalls) != 1 || brokerCalls[0] != hostWorkContextPath {
		t.Fatalf("broker calls = %v", brokerCalls)
	}
	h.assertNoStrayPaths(t)
}

func TestExchangeOperationRefreshesARejectedModuleWorkContextOnce(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	if _, err := client.ModuleWorkContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.gateway.revoke("module.token1")

	issued, err := client.ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "binding-1", Parent: token(t, "parent.token")})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Encoded() != "child.token" {
		t.Fatalf("issued token = %q", issued.Encoded())
	}
	calls := h.authority.snapshot()
	if len(calls) != 2 || calls[0].workContext[0] != "module.token1" || calls[1].workContext[0] != "module.token2" {
		t.Fatalf("authority calls = %+v, want token1 rejected then token2", calls)
	}
}

func TestExchangeOperationRetriesOnlyOnce(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	// Every module Work Context the broker issues is rejected.
	h.gateway.mu.Lock()
	h.gateway.rejectAll = true
	h.gateway.mu.Unlock()

	_, err := client.ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "binding-1", Parent: token(t, "parent.token")})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("error = %v, want unauthenticated after one retry", err)
	}
	if calls := h.authority.snapshot(); len(calls) != 2 {
		t.Fatalf("authority calls = %d, want exactly 2", len(calls))
	}
}

func TestExchangeOperationDoesNotRetryAPermissionDenial(t *testing.T) {
	h := newHost(t)
	seams := h.seams()
	client, err := New(seams, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	// The broker accepts the real token; the authority endpoint is handed a
	// wrong one by pointing this client's authority credential elsewhere.
	client.authority.token = "not-the-perimeter-token"
	_, err = client.ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "binding-1", Parent: token(t, "parent.token")})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("error = %v, want permission denied", err)
	}
	if calls := h.authority.snapshot(); len(calls) != 1 {
		t.Fatalf("authority calls = %d, want 1: a refresh cannot fix a perimeter refusal", len(calls))
	}
}

func TestExchangeOperationOverTLS(t *testing.T) {
	// httptest's certificate is valid for 127.0.0.1; reuse it for the gRPC
	// listener so the TLS path (ALPN h2) is exercised end to end.
	issuer := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(issuer.Close)
	gateway := newFakeGateway(t)
	authority := newFakeAuthority(t, gateway, credentials.NewServerTLSFromCert(&issuer.TLS.Certificates[0]))
	roots := x509.NewCertPool()
	roots.AddCert(issuer.Certificate())
	tlsConfig := issuer.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsConfig.RootCAs = roots

	client, err := New(Seams{
		Gateway:       gateway,
		Authority:     Authority{Address: authority.address, TLS: tlsConfig},
		InternalToken: testInternalToken,
	}, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	issued, err := client.ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "binding-1", Parent: token(t, "parent.token")})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Encoded() != "child.token" {
		t.Fatalf("issued token = %q", issued.Encoded())
	}
}

func TestExchangeOperationSurfacesABrokerRefusal(t *testing.T) {
	h := newHost(t)
	h.gateway.refuse[hostWorkContextPath] = refusal{http.StatusBadGateway, "work context unavailable"}
	_, err := h.client(t, moduleCredentials).ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "binding-1", Parent: token(t, "parent.token")})
	var be *BrokerError
	if !errors.As(err, &be) || be.StatusCode != http.StatusBadGateway {
		t.Fatalf("error = %v", err)
	}
	if calls := h.authority.snapshot(); len(calls) != 0 {
		t.Fatalf("no module Work Context, yet the authority endpoint was called %d times", len(calls))
	}
}

// TestExchangeOperationPresentsADelegationReferenceAndNoParent is the arm that
// carries long-running delegated work. It presents no capability at all, so the
// request must reach the host with the reference set and the parent field
// empty — a parent left in beside it would be a second authority the host is
// required to refuse.
func TestExchangeOperationPresentsADelegationReferenceAndNoParent(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)

	issued, err := client.ExchangeOperation(t.Context(), ExchangeRequest{
		BindingID:    "binding-1",
		DelegationID: "6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f",
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Encoded() != "child.token" {
		t.Fatalf("issued token = %q", issued.Encoded())
	}

	calls := h.authority.snapshot()
	if len(calls) != 1 {
		t.Fatalf("authority calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.method != "/saas.accounts.v1.ModuleCapabilitiesService/ExchangeDelegatedOperationAudience" {
		t.Fatalf("method = %q", call.method)
	}
	// The caller's own module Work Context still authenticates the call: a
	// reference is an identifier, and on its own it authorizes nothing.
	if len(call.workContext) != 1 || call.workContext[0] != "module.token1" {
		t.Fatalf("x-codefly-work-context = %v", call.workContext)
	}
	if len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken {
		t.Fatalf("x-codefly-internal-token = %v", call.internalToken)
	}
	if call.request.GetDelegationId() != "6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f" {
		t.Fatalf("delegation_id = %q", call.request.GetDelegationId())
	}
	if call.request.GetParentWorkContextToken() != "" {
		t.Fatalf("parent_work_context_token = %q, want empty on the reference arm", call.request.GetParentWorkContextToken())
	}
	h.assertNoStrayPaths(t)
}

// TestExchangeOperationRequiresExactlyOneAuthority: both arms and neither are
// refused here rather than at the host, and nothing is sent. The host enforces
// the same rule, so a request that cannot be authorized should not reach it.
func TestExchangeOperationRequiresExactlyOneAuthority(t *testing.T) {
	for _, c := range []struct {
		name    string
		request ExchangeRequest
	}{
		{"neither", ExchangeRequest{BindingID: "binding-1"}},
		{"both", ExchangeRequest{
			BindingID:    "binding-1",
			Parent:       token(t, "parent.token"),
			DelegationID: "6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f",
		}},
		{"no binding, with a reference", ExchangeRequest{
			DelegationID: "6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t)
			if _, err := h.client(t, moduleCredentials).ExchangeOperation(t.Context(), c.request); !errors.Is(err, ErrInvalidExchange) {
				t.Fatalf("err = %v, want ErrInvalidExchange", err)
			}
			if calls := h.authority.snapshot(); len(calls) != 0 {
				t.Fatalf("authority calls = %d, want none", len(calls))
			}
			// Not even the module capability is minted: nothing left the process.
			if brokerCalls, _, _, _ := h.gateway.snapshot(); len(brokerCalls) != 0 {
				t.Fatalf("broker calls = %v, want none", brokerCalls)
			}
		})
	}
}

// TestExchangeOperationRefreshesAModuleWorkContextOnTheReferenceArmToo: the
// retry resends the same request, so a reference must survive it. A retry that
// rebuilt the request from the parent field would silently send an empty
// authority on the second attempt and read as a host refusal.
func TestExchangeOperationRefreshesAModuleWorkContextOnTheReferenceArmToo(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	if _, err := client.ModuleWorkContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.gateway.revoke("module.token1")

	issued, err := client.ExchangeOperation(t.Context(), ExchangeRequest{
		BindingID:    "binding-1",
		DelegationID: "6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f",
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Encoded() != "child.token" {
		t.Fatalf("issued token = %q", issued.Encoded())
	}
	calls := h.authority.snapshot()
	if len(calls) != 2 || calls[0].workContext[0] != "module.token1" || calls[1].workContext[0] != "module.token2" {
		t.Fatalf("authority calls = %+v, want token1 rejected then token2", calls)
	}
	for i, call := range calls {
		if call.request.GetDelegationId() != "6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f" || call.request.GetParentWorkContextToken() != "" {
			t.Fatalf("attempt %d lost the reference: %+v", i, call.request)
		}
	}
}
