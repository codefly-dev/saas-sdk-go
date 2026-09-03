package workcontext

import (
	"context"
	"net/http"

	codefly "github.com/codefly-dev/sdk-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GRPCUnaryInterceptor verifies the Work Context carried in incoming gRPC
// metadata — under the same key sdk-go's WithGRPCExecutionContext writes —
// and stores the claims on the handler's context.
//
//	grpc.NewServer(grpc.ChainUnaryInterceptor(verifier.GRPCUnaryInterceptor()))
//
// It reads the Work Context only. sdk-go's GRPCExecutionContextFromIncoming
// also demands the operation-id carrier; a callee that wants that pairing can
// still call it from a handler.
func (v *Verifier) GRPCUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := v.authenticateGRPC(ctx)
		if err != nil {
			return nil, GRPCError(err)
		}
		return handler(ctx, req)
	}
}

// GRPCStreamInterceptor is GRPCUnaryInterceptor for streaming RPCs: the
// stream handler's ss.Context() carries the claims.
func (v *Verifier) GRPCStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := v.authenticateGRPC(ss.Context())
		if err != nil {
			return GRPCError(err)
		}
		if ctx != ss.Context() {
			ss = &serverStream{ServerStream: ss, ctx: ctx}
		}
		return handler(srv, ss)
	}
}

// serverStream overrides only Context so the handler sees the claims.
type serverStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *serverStream) Context() context.Context {
	return s.ctx
}

func (v *Verifier) authenticateGRPC(ctx context.Context) (context.Context, error) {
	// A nil MD (no incoming metadata) reads as no carrier, which authenticate
	// treats as missing — or pass-through in Optional mode.
	md, _ := metadata.FromIncomingContext(ctx)
	wc, err := v.authenticate(ctx, md.Get(codefly.WorkContextHeaderName))
	if err != nil {
		return ctx, err
	}
	if wc != nil {
		ctx = NewContext(ctx, wc)
	}
	return ctx, nil
}

// GRPCError maps a verification or scope error onto a gRPC status error:
// codes.Unauthenticated for missing / invalid, codes.PermissionDenied for
// audience / scope, codes.Internal otherwise. An error that already carries a
// status, and nil, come back unchanged.
func GRPCError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(grpcCode(err), err.Error())
}

func grpcCode(err error) codes.Code {
	switch HTTPStatus(err) {
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	default:
		return codes.Internal
	}
}
