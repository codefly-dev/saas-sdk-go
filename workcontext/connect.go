package workcontext

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	codefly "github.com/codefly-dev/sdk-go"
)

// ConnectInterceptor verifies the Work Context on every handler-side RPC —
// unary and streaming alike, so a streaming procedure is never the
// unauthenticated door — and stores the claims on the context the handler
// sees. Client-side calls pass through untouched: attaching a context to an
// outgoing call is codefly.AttachWorkContext's job.
//
//	mux.Handle(accountsv1connect.NewAuditServiceHandler(svc,
//		connect.WithInterceptors(verifier.ConnectInterceptor())))
func (v *Verifier) ConnectInterceptor() connect.Interceptor {
	return connectInterceptor{v: v}
}

type connectInterceptor struct {
	v *Verifier
}

func (i connectInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().IsClient {
			return next(ctx, req)
		}
		wc, err := i.v.authenticate(ctx, req.Header().Values(codefly.WorkContextHeaderName))
		if err != nil {
			return nil, ConnectError(err)
		}
		if wc != nil {
			ctx = NewContext(ctx, wc)
		}
		return next(ctx, req)
	}
}

func (i connectInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i connectInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		wc, err := i.v.authenticate(ctx, conn.RequestHeader().Values(codefly.WorkContextHeaderName))
		if err != nil {
			return ConnectError(err)
		}
		if wc != nil {
			ctx = NewContext(ctx, wc)
		}
		return next(ctx, conn)
	}
}

// ConnectError maps a verification or scope error onto a *connect.Error:
// CodeUnauthenticated for missing / invalid, CodePermissionDenied for
// audience / scope, CodeInternal otherwise. An error that already is a
// *connect.Error, and nil, come back unchanged.
func ConnectError(err error) error {
	if err == nil {
		return nil
	}
	var already *connect.Error
	if errors.As(err, &already) {
		return err
	}
	return connect.NewError(connectCode(err), err)
}

func connectCode(err error) connect.Code {
	switch HTTPStatus(err) {
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied
	default:
		return connect.CodeInternal
	}
}
