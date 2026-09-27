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
