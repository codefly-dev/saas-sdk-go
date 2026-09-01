package workcontext

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codefly "github.com/codefly-dev/sdk-go"
)

// EffectiveScopes returns the scopes that authorize this call: the owner's
// authority scopes on a direct call, or the last actor's granted scopes once a
// delegation chain is present. That is the rule sdk-go applies both when it
// attenuates on exchange and when it checks RequireWorkContextScope, so an
// agent acting for a user never authorizes from the user's wider grant.
func EffectiveScopes(wc *basev0.WorkContextV1) []*basev0.WorkScopeV1 {
	if actors := wc.GetActorChain(); len(actors) > 0 {
		return actors[len(actors)-1].GetGrantedScopes()
	}
	return wc.GetAuthorityScopes()
}

// HasScope reports whether the effective scope grants action on resourceKind.
//
// With no resourceID the question is "every resource of this kind": a grant
// with empty resource_ids (the wildcard) says yes, a grant restricted to
// explicit resource_ids says no. With resource IDs, each one must be granted —
// by the wildcard, or by appearing in the grant's resource_ids.
func HasScope(wc *basev0.WorkContextV1, resourceKind, action string, resourceID ...string) bool {
	return RequireScope(wc, resourceKind, action, resourceID...) == nil
}

// RequireScope is HasScope with the reason: ErrDenied naming the missing
// capability, or ErrInvalid when the claims no longer validate structurally
// (sdk-go re-validates on every check, so a proto mutated after Verify cannot
// widen itself).
func RequireScope(wc *basev0.WorkContextV1, resourceKind, action string, resourceID ...string) error {
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

// RequireScopeFromContext is RequireScope for handlers behind an interceptor,
// where the Work Context lives on the context: ErrMissing when none was
// verified (the interceptor is absent or Optional), else RequireScope. Map the
// result with ConnectError or GRPCError.
func RequireScopeFromContext(ctx context.Context, resourceKind, action string, resourceID ...string) error {
	wc, ok := FromContext(ctx)
	if !ok {
		return ErrMissing
	}
	return RequireScope(wc, resourceKind, action, resourceID...)
}
