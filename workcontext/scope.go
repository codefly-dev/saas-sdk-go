package workcontext

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codefly "github.com/codefly-dev/sdk-go"
)

// effectiveScopes returns the scopes that authorize this call: the owner's
// authority scopes on a direct call, or the last actor's granted scopes once a
// delegation chain is present. That is the rule sdk-go applies both when it
// attenuates on exchange and when it checks RequireWorkContextScope, so an
// agent acting for a user never authorizes from the user's wider grant.
func effectiveScopes(wc *basev0.WorkContextV1) []*basev0.WorkScopeV1 {
	if actors := wc.GetActorChain(); len(actors) > 0 {
		return actors[len(actors)-1].GetGrantedScopes()
	}
	return wc.GetAuthorityScopes()
}

// ScopesFromContext returns the effective scopes of the verified Work Context
// on ctx — the last actor's attenuated grant, or the owner's authority on a
// direct call. The second result is false when no verified context reached the
// handler (no interceptor, or Optional mode), the same as FromContext.
func ScopesFromContext(ctx context.Context) ([]*basev0.WorkScopeV1, bool) {
	wc, ok := FromContext(ctx)
	if !ok {
		return nil, false
	}
	// A copy of the slice: a caller reordering or replacing entries must not
	// mutate the verified claims later reads on this context authorize from.
	return append([]*basev0.WorkScopeV1(nil), effectiveScopes(wc)...), true
}

// HasScope reports whether the verified Work Context on ctx grants action on
// resourceKind. It is RequireScope reduced to a bool.
func HasScope(ctx context.Context, resourceKind, action string, resourceID ...string) bool {
	return RequireScope(ctx, resourceKind, action, resourceID...) == nil
}

// RequireScope authorizes a handler call against the verified Work Context on
// ctx: ErrMissing when none reached the handler (no interceptor, or Optional),
// ErrDenied naming the missing capability, or ErrInvalid when the claims no
// longer validate structurally (sdk-go re-validates on every check, so a proto
// mutated after Verify cannot widen itself). Map the result with HTTPStatus,
// ConnectError, or GRPCError.
//
// With no resourceID the question is "every resource of this kind": a grant
// with empty resource_ids (the wildcard) says yes, a grant restricted to
// explicit resource_ids says no. With resource IDs, each one must be granted —
// by the wildcard, or by appearing in the grant's resource_ids.
func RequireScope(ctx context.Context, resourceKind, action string, resourceID ...string) error {
	wc, ok := FromContext(ctx)
	if !ok {
		return ErrMissing
	}
	if len(resourceID) == 0 {
		return codefly.RequireWorkContextScope(wc, codefly.WorkContextScopeRequirement{
			ResourceKind: resourceKind,
			Action:       action,
		})
	}
	for _, id := range resourceID {
		if err := codefly.RequireWorkContextScope(wc, codefly.WorkContextScopeRequirement{
			ResourceKind: resourceKind,
			Action:       action,
			ResourceID:   id,
		}); err != nil {
			return err
		}
	}
	return nil
}
