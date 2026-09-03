package workcontext_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	codefly "github.com/codefly-dev/sdk-go"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
	"github.com/codefly-dev/saas-sdk-go/workcontext"
)

// The Connect tests run the interceptor inside real generated handlers — the
// unary AuditService and the one server-streaming RPC in the accounts proto,
// DelegationService.WaitForDelegation — so both wrap paths are exercised end
// to end through the actual stubs.

type auditHandler struct {
	accountsv1connect.UnimplementedAuditServiceHandler
	seen *seen
}

func (h *auditHandler) QueryAuditLog(ctx context.Context, _ *connect.Request[v1.QueryAuditLogRequest]) (*connect.Response[v1.QueryAuditLogResponse], error) {
	h.seen.record(ctx)
	return connect.NewResponse(&v1.QueryAuditLogResponse{}), nil
}

type delegationHandler struct {
	accountsv1connect.UnimplementedDelegationServiceHandler
	seen *seen
}

func (h *delegationHandler) WaitForDelegation(ctx context.Context, _ *connect.Request[v1.WaitForDelegationRequest], _ *connect.ServerStream[v1.DelegationEvent]) error {
	h.seen.record(ctx)
	return nil
}

type connectFixture struct {
	audit       accountsv1connect.AuditServiceClient
	delegations accountsv1connect.DelegationServiceClient
	seen        *seen
}

func newConnectFixture(t *testing.T, v *workcontext.Verifier) connectFixture {
	t.Helper()
	s := &seen{}
	mux := http.NewServeMux()
	mux.Handle(accountsv1connect.NewAuditServiceHandler(&auditHandler{seen: s}, connect.WithInterceptors(v.ConnectInterceptor())))
	mux.Handle(accountsv1connect.NewDelegationServiceHandler(&delegationHandler{seen: s}, connect.WithInterceptors(v.ConnectInterceptor())))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return connectFixture{
		audit:       accountsv1connect.NewAuditServiceClient(srv.Client(), srv.URL),
		delegations: accountsv1connect.NewDelegationServiceClient(srv.Client(), srv.URL),
		seen:        s,
	}
}

func withCarrier[T any](req *connect.Request[T], tokens ...string) *connect.Request[T] {
	for _, token := range tokens {
		req.Header().Add(codefly.WorkContextHeaderName, token)
	}
	return req
}

func TestConnectInterceptorUnary(t *testing.T) {
	f := newConnectFixture(t, newVerifier(t, workcontext.Config{Keys: staticKeys()}))
	valid := mintValid(t)
	cases := []struct {
		name   string
		tokens []string
		code   connect.Code // zero means success
		called bool
	}{
		{"valid", []string{valid}, 0, true},
		{"missing", nil, connect.CodeUnauthenticated, false},
		{"garbage", []string{"not.a-token"}, connect.CodeUnauthenticated, false},
		{"wrong audience", []string{mint(t, newSigner(t, signerOptions{}), "someone-else")}, connect.CodePermissionDenied, false},
		{"two carriers", []string{valid, valid}, connect.CodeUnauthenticated, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.seen.reset()
			_, err := f.audit.QueryAuditLog(context.Background(), withCarrier(connect.NewRequest(&v1.QueryAuditLogRequest{}), tc.tokens...))
			switch {
			case tc.code == 0 && err != nil:
				t.Errorf("err = %v, want success", err)
			case tc.code != 0 && connect.CodeOf(err) != tc.code:
				t.Errorf("err = %v (code %v), want code %v", err, connect.CodeOf(err), tc.code)
			}
			f.seen.expect(t, tc.called, tc.called)
		})
	}
}

func TestConnectInterceptorStreamingHandler(t *testing.T) {
	f := newConnectFixture(t, newVerifier(t, workcontext.Config{Keys: staticKeys()}))

	// streamErr drains a server stream and returns whichever error the
	// handler side produced — Connect surfaces it on Receive/Err, not on the
	// call itself.
	streamErr := func(stream *connect.ServerStreamForClient[v1.DelegationEvent], err error) error {
		if err != nil {
			return err
		}
		for stream.Receive() {
		}
		return stream.Err()
	}

	f.seen.reset()
	err := streamErr(f.delegations.WaitForDelegation(context.Background(), withCarrier(connect.NewRequest(&v1.WaitForDelegationRequest{}), mintValid(t))))
	if err != nil {
		t.Errorf("valid: %v", err)
	}
	f.seen.expect(t, true, true)

	f.seen.reset()
	err = streamErr(f.delegations.WaitForDelegation(context.Background(), connect.NewRequest(&v1.WaitForDelegationRequest{})))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("missing: err = %v, want CodeUnauthenticated", err)
	}
	f.seen.expect(t, false, false)

	f.seen.reset()
	err = streamErr(f.delegations.WaitForDelegation(context.Background(), withCarrier(connect.NewRequest(&v1.WaitForDelegationRequest{}), mint(t, newSigner(t, signerOptions{}), "someone-else"))))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("wrong audience: err = %v, want CodePermissionDenied", err)
	}
	f.seen.expect(t, false, false)
}

func TestConnectInterceptorOptional(t *testing.T) {
	f := newConnectFixture(t, newVerifier(t, workcontext.Config{Keys: staticKeys(), Optional: true}))

	if _, err := f.audit.QueryAuditLog(context.Background(), connect.NewRequest(&v1.QueryAuditLogRequest{})); err != nil {
		t.Errorf("missing under Optional: %v", err)
	}
	f.seen.expect(t, true, false)

	f.seen.reset()
	_, err := f.audit.QueryAuditLog(context.Background(), withCarrier(connect.NewRequest(&v1.QueryAuditLogRequest{}), "not.a-token"))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("garbage under Optional: err = %v, want CodeUnauthenticated", err)
	}
	f.seen.expect(t, false, false)
}

func TestConnectInterceptorIgnoresClientSideCalls(t *testing.T) {
	// The server has no interceptor; the client has ours and sends no carrier.
	// If the interceptor verified on the client side the call would fail
	// Unauthenticated before it ever left the process.
	s := &seen{}
	mux := http.NewServeMux()
	mux.Handle(accountsv1connect.NewAuditServiceHandler(&auditHandler{seen: s}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	client := accountsv1connect.NewAuditServiceClient(srv.Client(), srv.URL, connect.WithInterceptors(v.ConnectInterceptor()))

	if _, err := client.QueryAuditLog(context.Background(), connect.NewRequest(&v1.QueryAuditLogRequest{})); err != nil {
		t.Errorf("client-side call intercepted: %v", err)
	}
	s.expect(t, true, false)
}

func TestConnectInterceptorIgnoresClientSideStreams(t *testing.T) {
	// The server has no interceptor; the streaming client has ours and sends
	// no carrier. If WrapStreamingClient verified, the call would fail
	// Unauthenticated before leaving the process.
	s := &seen{}
	mux := http.NewServeMux()
	mux.Handle(accountsv1connect.NewDelegationServiceHandler(&delegationHandler{seen: s}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	client := accountsv1connect.NewDelegationServiceClient(srv.Client(), srv.URL, connect.WithInterceptors(v.ConnectInterceptor()))

	stream, err := client.WaitForDelegation(context.Background(), connect.NewRequest(&v1.WaitForDelegationRequest{}))
	if err != nil {
		t.Fatalf("client-side stream intercepted: %v", err)
	}
	for stream.Receive() {
	}
	if err := stream.Err(); err != nil {
		t.Errorf("client-side stream intercepted: %v", err)
	}
	s.expect(t, true, false)
}

func TestConnectErrorMapsEverySentinel(t *testing.T) {
	cases := map[connect.Code][]error{
		connect.CodeUnauthenticated:  {workcontext.ErrMissing, workcontext.ErrInvalid, wrap(workcontext.ErrInvalid)},
		connect.CodePermissionDenied: {workcontext.ErrAudience, workcontext.ErrDenied},
		connect.CodeInternal:         {errors.New("something else")},
	}
	for want, errs := range cases {
		for _, err := range errs {
			mapped := workcontext.ConnectError(err)
			if got := connect.CodeOf(mapped); got != want {
				t.Errorf("ConnectError(%v) = %v (code %v), want %v", err, mapped, got, want)
			}
			if !errors.Is(mapped, err) {
				t.Errorf("ConnectError(%v) lost the cause: %v", err, mapped)
			}
		}
	}
	if workcontext.ConnectError(nil) != nil {
		t.Error("ConnectError(nil) should be nil")
	}

	// An error that already is a *connect.Error passes through unchanged.
	already := connect.NewError(connect.CodeNotFound, errors.New("already mapped"))
	if workcontext.ConnectError(already) != already {
		t.Error("ConnectError rewrapped an existing *connect.Error")
	}
}
