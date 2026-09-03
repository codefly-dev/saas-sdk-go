package workcontext

import (
	"errors"
	"net/http"

	codefly "github.com/codefly-dev/sdk-go"
)

// HTTPMiddleware verifies the Work Context on every request and hands the
// claims to next through the request context (see FromContext). A missing or
// unverifiable context is a 401, a sound context for another service a 403;
// in Optional mode a missing context passes through unauthenticated.
func (v *Verifier) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wc, err := v.authenticate(r.Context(), r.Header.Values(codefly.WorkContextHeaderName))
		if err != nil {
			http.Error(w, err.Error(), HTTPStatus(err))
			return
		}
		if wc != nil {
			r = r.WithContext(NewContext(r.Context(), wc))
		}
		next.ServeHTTP(w, r)
	})
}

// RequireScopeHTTP guards a route behind one kind-wide scope: 401 when no
// verified Work Context reached it (no middleware, or Optional), 403 when the
// context lacks the scope. Per-resource checks belong in the handler, with
// RequireScope, where the resource id is known.
func RequireScopeHTTP(resourceKind, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := RequireScope(r.Context(), resourceKind, action); err != nil {
				http.Error(w, err.Error(), HTTPStatus(err))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// HTTPStatus maps a verification or scope error onto the status the
// middleware writes: 401 for a missing or unverifiable context, 403 for a
// verified one that is not for this service or lacks the scope, 200 for nil,
// and 500 for an error this package did not produce.
func HTTPStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrAudience), errors.Is(err, ErrDenied):
		return http.StatusForbidden
	case errors.Is(err, ErrMissing), errors.Is(err, ErrInvalid):
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}
