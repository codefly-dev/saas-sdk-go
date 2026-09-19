package moduleauthority

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	codefly "github.com/codefly-dev/sdk-go"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

type testGateway struct {
	url    string
	client *http.Client
}

func (g testGateway) BaseURL() string          { return g.url }
func (g testGateway) HTTPClient() *http.Client { return g.client }

type authorityHandler struct {
	accountsv1connect.UnimplementedModuleCapabilitiesServiceHandler

	mu            sync.Mutex
	mints         int
	exchanges     int
	rejectFirst   bool
	lastRequest   *v1.ModuleExchangeDelegatedOperationAudienceRequest
	moduleHeaders []string
}

func (h *authorityHandler) MintModuleWorkContext(_ context.Context, req *connect.Request[v1.ModuleMintWorkContextRequest]) (*connect.Response[v1.ModuleMintWorkContextResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if req.Msg.GetPrefix() != "runtime" || req.Msg.GetSecret() != "projected-secret" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	h.mints++
	return connect.NewResponse(&v1.ModuleMintWorkContextResponse{
		Token:     "module.token" + string(rune('0'+h.mints)),
		ExpiresAt: timestamppb.New(time.Now().Add(time.Hour)),
	}), nil
}

func (h *authorityHandler) ExchangeDelegatedOperationAudience(_ context.Context, req *connect.Request[v1.ModuleExchangeDelegatedOperationAudienceRequest]) (*connect.Response[v1.IssuedWorkContext], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.exchanges++
	h.lastRequest = req.Msg
	h.moduleHeaders = append(h.moduleHeaders, req.Header().Get(codefly.WorkContextHeaderName))
	if h.rejectFirst && h.exchanges == 1 {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	return connect.NewResponse(&v1.IssuedWorkContext{Token: "child.token"}), nil
}

func newTestClient(t *testing.T, handler *authorityHandler) *Client {
	t.Helper()
	path, serverHandler := accountsv1connect.NewModuleCapabilitiesServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, serverHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return New(testGateway{url: server.URL, client: server.Client()}, Credentials{
		Prefix: "runtime",
		Secret: "projected-secret",
	})
}

func token(t *testing.T, encoded string) codefly.WorkContextToken {
	t.Helper()
	token, err := codefly.ParseWorkContextToken(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestExchangeOperationUsesInstalledBindingAndCachesModuleAuthority(t *testing.T) {
	handler := &authorityHandler{}
	client := newTestClient(t, handler)
	request := ExchangeRequest{
		BindingID: "binding-1",
		Parent:    token(t, "parent.token"),
		Lookup:    true,
	}

	for range 2 {
		issued, err := client.ExchangeOperation(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if issued.Encoded() != "child.token" {
			t.Fatalf("issued token = %q", issued.Encoded())
		}
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.mints != 1 || handler.exchanges != 2 {
		t.Fatalf("mints/exchanges = %d/%d, want 1/2", handler.mints, handler.exchanges)
	}
	if handler.lastRequest.GetBindingId() != "binding-1" || handler.lastRequest.GetParentWorkContextToken() != "parent.token" || !handler.lastRequest.GetLookup() {
		t.Fatalf("unexpected exchange request: %+v", handler.lastRequest)
	}
	for _, header := range handler.moduleHeaders {
		if header != "module.token1" {
			t.Fatalf("module Work Context header = %q", header)
		}
	}
}

func TestExchangeOperationRefreshesRejectedModuleAuthorityOnce(t *testing.T) {
	handler := &authorityHandler{rejectFirst: true}
	client := newTestClient(t, handler)

	issued, err := client.ExchangeOperation(context.Background(), ExchangeRequest{
		BindingID: "binding-1",
		Parent:    token(t, "parent.token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Encoded() != "child.token" {
		t.Fatalf("issued token = %q", issued.Encoded())
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.mints != 2 || handler.exchanges != 2 {
		t.Fatalf("mints/exchanges = %d/%d, want 2/2", handler.mints, handler.exchanges)
	}
	if handler.moduleHeaders[0] != "module.token1" || handler.moduleHeaders[1] != "module.token2" {
		t.Fatalf("module headers = %#v", handler.moduleHeaders)
	}
}

func TestExchangeOperationRejectsIncompleteInputWithoutNetwork(t *testing.T) {
	handler := &authorityHandler{}
	client := newTestClient(t, handler)

	if _, err := client.ExchangeOperation(context.Background(), ExchangeRequest{}); err != ErrInvalidExchange {
		t.Fatalf("error = %v, want %v", err, ErrInvalidExchange)
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.mints != 0 || handler.exchanges != 0 {
		t.Fatalf("unexpected calls: %d/%d", handler.mints, handler.exchanges)
	}
}

func TestModuleWorkContextRejectsMissingCredentialsWithoutNetwork(t *testing.T) {
	handler := &authorityHandler{}
	client := newTestClient(t, handler)
	client.credentials = Credentials{}

	if _, err := client.ModuleWorkContext(context.Background()); err != ErrInvalidCredentials {
		t.Fatalf("error = %v, want %v", err, ErrInvalidCredentials)
	}
}
