package workcontext_test

import (
	"context"
	"errors"
	"testing"

	codefly "github.com/codefly-dev/sdk-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/codefly-dev/saas-sdk-go/workcontext"
)

// The gRPC interceptors are plain functions over incoming metadata, so the
// tests call them directly with a metadata-bearing context rather than
// standing up a server; the last test proves the metadata key is the one
// sdk-go writes on the caller's side.

func incoming(tokens ...string) context.Context {
	md := metadata.MD{}
	for _, token := range tokens {
		md.Append(codefly.WorkContextHeaderName, token)
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *fakeStream) Context() context.Context { return s.ctx }

func TestGRPCUnaryInterceptor(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	valid := mintValid(t)
	cases := []struct {
		name   string
		ctx    context.Context
		code   codes.Code
		called bool
	}{
		{"valid", incoming(valid), codes.OK, true},
		{"no metadata at all", context.Background(), codes.Unauthenticated, false},
		{"metadata without the carrier", incoming(), codes.Unauthenticated, false},
		{"garbage", incoming("not.a-token"), codes.Unauthenticated, false},
		{"wrong audience", incoming(mint(t, newSigner(t, signerOptions{}), "someone-else")), codes.PermissionDenied, false},
		{"two carriers", incoming(valid, valid), codes.Unauthenticated, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &seen{}
			resp, err := v.GRPCUnaryInterceptor()(tc.ctx, "request", &grpc.UnaryServerInfo{FullMethod: "/lastlogin.v1.Logins/List"}, func(ctx context.Context, _ any) (any, error) {
				s.record(ctx)
				return "response", nil
			})
			if got := status.Code(err); got != tc.code {
				t.Errorf("err = %v (code %v), want %v", err, got, tc.code)
			}
			if tc.called && resp != "response" {
				t.Errorf("resp = %v, want the handler's", resp)
			}
			s.expect(t, tc.called, tc.called)
		})
	}
}

func TestGRPCStreamInterceptor(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	handler := func(s *seen) grpc.StreamHandler {
		return func(_ any, ss grpc.ServerStream) error {
			s.record(ss.Context())
			return nil
		}
	}

	s := &seen{}
	if err := v.GRPCStreamInterceptor()(nil, &fakeStream{ctx: incoming(mintValid(t))}, &grpc.StreamServerInfo{}, handler(s)); err != nil {
		t.Errorf("valid: %v", err)
	}
	s.expect(t, true, true)

	s = &seen{}
	err := v.GRPCStreamInterceptor()(nil, &fakeStream{ctx: incoming()}, &grpc.StreamServerInfo{}, handler(s))
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("missing: err = %v, want Unauthenticated", err)
	}
	s.expect(t, false, false)
}

func TestGRPCInterceptorOptional(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys(), Optional: true})

	s := &seen{}
	if _, err := v.GRPCUnaryInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, _ any) (any, error) {
		s.record(ctx)
		return nil, nil
	}); err != nil {
		t.Errorf("missing under Optional: %v", err)
	}
	s.expect(t, true, false)

	s = &seen{}
	_, err := v.GRPCUnaryInterceptor()(incoming("not.a-token"), nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, _ any) (any, error) {
		s.record(ctx)
		return nil, nil
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("garbage under Optional: err = %v, want Unauthenticated", err)
	}
	s.expect(t, false, false)
}

func TestGRPCInterceptorReadsTheCarrierSDKGoWrites(t *testing.T) {
	// A caller attaches its execution context with sdk-go; the metadata that
	// lands on the wire is what the callee's interceptor must read.
	token, err := codefly.ParseWorkContextToken(mintValid(t))
	if err != nil {
		t.Fatalf("ParseWorkContextToken: %v", err)
	}
	execution, err := codefly.NewExecutionContext(token, "list-logins")
	if err != nil {
		t.Fatalf("NewExecutionContext: %v", err)
	}
	outgoing, err := codefly.WithGRPCExecutionContext(context.Background(), execution)
	if err != nil {
		t.Fatalf("WithGRPCExecutionContext: %v", err)
	}
	md, _ := metadata.FromOutgoingContext(outgoing)

	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	s := &seen{}
	if _, err := v.GRPCUnaryInterceptor()(metadata.NewIncomingContext(context.Background(), md), nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, _ any) (any, error) {
		s.record(ctx)
		return nil, nil
	}); err != nil {
		t.Errorf("sdk-go carrier rejected: %v", err)
	}
	s.expect(t, true, true)
}

func TestGRPCErrorMapsEverySentinel(t *testing.T) {
	cases := map[codes.Code][]error{
		codes.Unauthenticated:  {workcontext.ErrMissing, workcontext.ErrInvalid, wrap(workcontext.ErrInvalid)},
		codes.PermissionDenied: {workcontext.ErrAudience, workcontext.ErrDenied},
		codes.Internal:         {errors.New("something else")},
		codes.NotFound:         {status.Error(codes.NotFound, "already mapped")},
	}
	for want, errs := range cases {
		for _, err := range errs {
			if got := status.Code(workcontext.GRPCError(err)); got != want {
				t.Errorf("GRPCError(%v) code = %v, want %v", err, got, want)
			}
		}
	}
	if workcontext.GRPCError(nil) != nil {
		t.Error("GRPCError(nil) should be nil")
	}
}
