package moduleauthority

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/codefly-dev/core/wool"
	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
)

type bridgeGateway struct{ server *httptest.Server }

func (g bridgeGateway) BaseURL() string          { return g.server.URL }
func (g bridgeGateway) HTTPClient() *http.Client { return g.server.Client() }

func TestGatewayAuthorityExchangesThroughOnlyTheGateway(t *testing.T) {
	broker := newFakeGateway(t)
	const procedure = "/modules/_authority/saas.accounts.v1.ModuleCapabilitiesService/ExchangeDelegatedOperationAudience"
	var calls atomic.Int32
	bridge := connect.NewUnaryHandler(procedure, func(ctx context.Context, req *connect.Request[v1.ModuleExchangeDelegatedOperationAudienceRequest]) (*connect.Response[v1.IssuedWorkContext], error) {
		calls.Add(1)
		if req.Header().Get("Content-Type") != "application/proto" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bridge requires canonical binary protobuf"))
		}
		if req.Header().Get(internalTokenHeader) != testInternalToken || req.Header().Get(wool.WorkContextHeader) != sealedToken("module.token1") {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid fixture proof"))
		}
		if req.Msg.GetBindingId() != "binding-fixture" || req.Msg.GetParentWorkContextToken() != sealedToken("parent.token") || !req.Msg.GetLookup() {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("intent changed"))
		}
		return connect.NewResponse(&v1.IssuedWorkContext{Token: sealedToken("child.token")}), nil
	})
	mux := http.NewServeMux()
	mux.Handle(procedure, bridge)
	mux.HandleFunc("/", broker.serve)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	c, err := NewGatewayAuthority(Seams{Gateway: bridgeGateway{server}, InternalToken: testInternalToken}, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	issued, err := c.ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "binding-fixture", Parent: sealedToken("parent.token"), Lookup: true})
	if err != nil {
		t.Fatal(err)
	}
	if issued != sealedToken("child.token") || calls.Load() != 1 {
		t.Fatal("exchange did not preserve its outcome")
	}
	paths, stray, _, _ := broker.snapshot()
	if len(paths) != 1 || paths[0] != hostWorkContextPath || len(stray) != 0 {
		t.Fatalf("unexpected broker calls: %v, stray: %v", paths, stray)
	}
}

func TestGatewayAuthorityRejectsAmbiguousTransport(t *testing.T) {
	// Every seam is otherwise valid: a missing token or gateway must not make
	// this test pass when the explicit transport-selection guard is removed.
	h := newHost(t)
	_, err := NewGatewayAuthority(h.seams(), moduleCredentials)
	if !errors.Is(err, ErrInvalidSeams) {
		t.Fatalf("got %v, want invalid seams", err)
	}
	if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 0 || len(h.authority.snapshot()) != 0 {
		t.Fatal("ambiguous transport must fail before any request")
	}
}

func TestGatewayAuthorityDoesNotFollowRedirects(t *testing.T) {
	broker := newFakeGateway(t)
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == hostWorkContextPath {
			broker.serve(w, r)
			return
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	c, err := NewGatewayAuthority(Seams{Gateway: bridgeGateway{server}, InternalToken: testInternalToken}, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "binding-fixture", Parent: sealedToken("parent.token")})
	if err == nil || received.Load() != 0 {
		t.Fatalf("redirected capability: err=%v, target calls=%d", err, received.Load())
	}
}
