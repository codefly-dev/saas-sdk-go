package workcontext

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

type contextKey struct{}

// NewContext returns a copy of ctx carrying a verified Work Context. The
// middleware and interceptors call it; in-process callers and tests can too.
// Only store claims that came out of Verify — a handler that authorizes from a
// hand-built proto is authorizing from nothing.
func NewContext(ctx context.Context, wc *basev0.WorkContextV1) context.Context {
	return context.WithValue(ctx, contextKey{}, wc)
}

// FromContext returns the verified Work Context stored on ctx, or false when
// there is none — which, behind the middleware, only happens in Optional mode.
func FromContext(ctx context.Context) (*basev0.WorkContextV1, bool) {
	wc, ok := ctx.Value(contextKey{}).(*basev0.WorkContextV1)
	return wc, ok && wc != nil
}
