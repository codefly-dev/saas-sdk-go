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
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

type sourceContextHandler struct {
	accountsv1connect.UnimplementedModuleCapabilitiesServiceHandler

	mu      sync.Mutex
	mints   int
	last    *v1.ModuleMintSourceOperationContextRequest
	refuse  error
	source  string
	expires time.Time
}

func (h *sourceContextHandler) MintSourceOperationContext(_ context.Context, req *connect.Request[v1.ModuleMintSourceOperationContextRequest]) (*connect.Response[v1.ModuleMintSourceOperationContextResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mints++
	h.last = req.Msg
	if h.refuse != nil {
		return nil, h.refuse
	}
	source := req.Msg.GetSourceId()
	if h.source != "" {
		source = h.source
	}
	return connect.NewResponse(&v1.ModuleMintSourceOperationContextResponse{
		Token:            "operation.token",
		ExpiresAt:        timestamppb.New(h.expires),
		PrincipalId:      "00000000-0000-4000-8000-00000000beef",
		Tenant:           "22222222-2222-4222-8222-222222222222",
		Audience:         "runtime",
		Binding:          "runtime-admission",
		DelegationId:     "33333333-3333-4333-8333-333333333333",
		SourceId:         source,
		OwnerPrincipalId: "44444444-4444-4444-8444-444444444444",
	}), nil
}

func newSourceContextClient(t *testing.T, handler *sourceContextHandler, credentials Credentials) *Client {
	t.Helper()
	path, serverHandler := accountsv1connect.NewModuleCapabilitiesServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, serverHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return New(testGateway{url: server.URL, client: server.Client()}, credentials)
}

const exampleSource = "55555555-5555-4555-8555-555555555555"

func TestMintSourceOperationContextNamesOnlyTheSource(t *testing.T) {
	handler := &sourceContextHandler{expires: time.Now().Add(time.Minute)}
	client := newSourceContextClient(t, handler, moduleCredentials)
	got, err := client.MintSourceOperationContext(t.Context(), exampleSource)
	if err != nil {
		t.Fatal(err)
	}
	if handler.last.GetSourceId() != exampleSource || handler.last.GetDelegationId() != "" || handler.last.GetPrefix() != moduleCredentials.Prefix || handler.last.GetSecret() != moduleCredentials.Secret {
		t.Fatalf("request: %+v", handler.last)
	}
	if got.Tenant != "22222222-2222-4222-8222-222222222222" || got.OwnerPrincipalID != "44444444-4444-4444-8444-444444444444" || got.DelegationID == "" || got.SourceID != exampleSource || got.Audience != "runtime" {
		t.Fatalf("context: %+v", got)
	}
	if _, err := client.MintSourceOperationContext(t.Context(), exampleSource); err != nil || handler.mints != 2 {
		t.Fatalf("a source context is minted per call, never cached: mints=%d err=%v", handler.mints, err)
	}
}

func refusal(code connect.Code, reason string) error {
	err := connect.NewError(code, errors.New(reason))
	if detail, derr := connect.NewErrorDetail(&errdetails.ErrorInfo{Reason: reason, Domain: delegationErrorDomain}); derr == nil {
		err.AddDetail(detail)
	}
	return err
}

func TestMintSourceOperationContextTypesEveryDelegationRefusal(t *testing.T) {
	for _, tc := range []struct {
		refuse error
		want   error
	}{
		{refusal(connect.CodeFailedPrecondition, "DELEGATION_MISSING"), ErrDelegationMissing},
		{refusal(connect.CodePermissionDenied, "DELEGATION_REVOKED"), ErrDelegationRevoked},
		{refusal(connect.CodePermissionDenied, "DELEGATION_INVALID"), ErrDelegationInvalid},
	} {
		handler := &sourceContextHandler{refuse: tc.refuse, expires: time.Now().Add(time.Minute)}
		_, err := newSourceContextClient(t, handler, moduleCredentials).MintSourceOperationContext(t.Context(), exampleSource)
		if !errors.Is(err, tc.want) {
			t.Fatalf("want %v, got %v", tc.want, err)
		}
	}
	// Without the typed reason a refusal is not claimed to be a delegation
	// refusal: an unproven module or an outage is no reason to tell a person to
	// reconnect.
	for _, plain := range []error{connect.NewError(connect.CodePermissionDenied, nil), connect.NewError(connect.CodeUnavailable, nil), connect.NewError(connect.CodeUnauthenticated, nil)} {
		handler := &sourceContextHandler{refuse: plain, expires: time.Now().Add(time.Minute)}
		_, err := newSourceContextClient(t, handler, moduleCredentials).MintSourceOperationContext(t.Context(), exampleSource)
		if err == nil || errors.Is(err, ErrDelegationMissing) || errors.Is(err, ErrDelegationRevoked) || errors.Is(err, ErrDelegationInvalid) {
			t.Fatalf("%v was typed as a delegation refusal: %v", plain, err)
		}
	}
}

func TestMintSourceOperationContextRefusesAnAnswerForAnotherSourceOrExpired(t *testing.T) {
	handler := &sourceContextHandler{source: "66666666-6666-4666-8666-666666666666", expires: time.Now().Add(time.Minute)}
	if _, err := newSourceContextClient(t, handler, moduleCredentials).MintSourceOperationContext(t.Context(), exampleSource); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("a context for another source: %v", err)
	}
	handler = &sourceContextHandler{expires: time.Now().Add(-time.Second)}
	if _, err := newSourceContextClient(t, handler, moduleCredentials).MintSourceOperationContext(t.Context(), exampleSource); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("an expired context: %v", err)
	}
}

func TestMintSourceOperationContextRejectsIncompleteInputWithoutNetwork(t *testing.T) {
	handler := &sourceContextHandler{expires: time.Now().Add(time.Minute)}
	if _, err := newSourceContextClient(t, handler, moduleCredentials).MintSourceOperationContext(t.Context(), ""); !errors.Is(err, ErrInvalidExchange) {
		t.Fatalf("no source: %v", err)
	}
	if _, err := newSourceContextClient(t, handler, Credentials{Prefix: "documents"}).MintSourceOperationContext(t.Context(), exampleSource); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("no secret: %v", err)
	}
	if handler.mints != 0 {
		t.Fatalf("incomplete input reached the network %d times", handler.mints)
	}
}
