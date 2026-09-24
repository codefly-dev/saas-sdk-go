package moduleauthority

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

type operationContextHandler struct {
	accountsv1connect.UnimplementedModuleCapabilitiesServiceHandler

	mu        sync.Mutex
	mints     int
	last      *v1.ModuleMintOperationContextRequest
	expiresAt time.Time
	binding   string
	refuse    connect.Code
}

func (h *operationContextHandler) MintModuleOperationContext(_ context.Context, req *connect.Request[v1.ModuleMintOperationContextRequest]) (*connect.Response[v1.ModuleMintOperationContextResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mints++
	h.last = req.Msg
	if h.refuse != 0 {
		return nil, connect.NewError(h.refuse, nil)
	}
	binding := req.Msg.GetBinding()
	if h.binding != "" {
		binding = h.binding
	}
	return connect.NewResponse(&v1.ModuleMintOperationContextResponse{
		Token:       "operation.token",
		ExpiresAt:   timestamppb.New(h.expiresAt),
		PrincipalId: "00000000-0000-4000-8000-00000000beef",
		Tenant:      "11111111-1111-4111-8111-111111111111",
		Audience:    "modelservice",
		Binding:     binding,
	}), nil
}

func newOperationContextClient(t *testing.T, handler *operationContextHandler, credentials Credentials) *Client {
	t.Helper()
	path, serverHandler := accountsv1connect.NewModuleCapabilitiesServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, serverHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return New(testGateway{url: server.URL, client: server.Client()}, credentials)
}

var moduleCredentials = Credentials{Prefix: "documents", Secret: "projected-secret"}

func TestMintModuleOperationContextSendsOnlyTheBindingAndNeverCaches(t *testing.T) {
	handler := &operationContextHandler{expiresAt: time.Now().Add(time.Minute)}
	client := newOperationContextClient(t, handler, moduleCredentials)

	for range 2 {
		issued, err := client.MintModuleOperationContext(context.Background(), "model")
		if err != nil {
			t.Fatal(err)
		}
		if issued.Token.Encoded() != "operation.token" || issued.Audience != "modelservice" || issued.BindingID != "model" ||
			issued.Tenant != "11111111-1111-4111-8111-111111111111" || issued.PrincipalID != "00000000-0000-4000-8000-00000000beef" {
			t.Fatalf("unexpected operation context: %+v", issued)
		}
		if !issued.ExpiresAt.Equal(handler.expiresAt) {
			t.Fatalf("expires at = %v, want %v", issued.ExpiresAt, handler.expiresAt)
		}
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.mints != 2 {
		t.Fatalf("mints = %d, want 2: an operation context is never cached", handler.mints)
	}
	if handler.last.GetPrefix() != "documents" || handler.last.GetSecret() != "projected-secret" || handler.last.GetBinding() != "model" {
		t.Fatalf("unexpected mint request: %+v", handler.last)
	}
}

func TestMintModuleOperationContextRejectsIncompleteInputWithoutNetwork(t *testing.T) {
	handler := &operationContextHandler{expiresAt: time.Now().Add(time.Minute)}

	if _, err := newOperationContextClient(t, handler, moduleCredentials).MintModuleOperationContext(context.Background(), ""); !errors.Is(err, ErrInvalidExchange) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidExchange)
	}
	if _, err := newOperationContextClient(t, handler, Credentials{Prefix: "documents"}).MintModuleOperationContext(context.Background(), "model"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidCredentials)
	}
	if handler.mints != 0 {
		t.Fatalf("mints = %d, want 0", handler.mints)
	}
}

func TestMintModuleOperationContextSurfacesRefusalCode(t *testing.T) {
	handler := &operationContextHandler{refuse: connect.CodePermissionDenied}
	client := newOperationContextClient(t, handler, moduleCredentials)

	_, err := client.MintModuleOperationContext(context.Background(), "interactive")
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
	}
}

func TestMintModuleOperationContextRejectsAnInvalidCapability(t *testing.T) {
	for name, handler := range map[string]*operationContextHandler{
		"expired":         {expiresAt: time.Now().Add(-time.Second)},
		"another binding": {expiresAt: time.Now().Add(time.Minute), binding: "other"},
	} {
		t.Run(name, func(t *testing.T) {
			client := newOperationContextClient(t, handler, moduleCredentials)
			if _, err := client.MintModuleOperationContext(context.Background(), "model"); !errors.Is(err, ErrInvalidCapability) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidCapability)
			}
		})
	}
}
