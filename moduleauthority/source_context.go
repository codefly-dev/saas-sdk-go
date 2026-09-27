package moduleauthority

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	codefly "github.com/codefly-dev/sdk-go/workcontext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
)

// The delegation refusals SaaS names in a google.rpc.ErrorInfo of this domain.
// Each is a denial a caller must surface, never an outage to retry, and none
// of them may be answered by acting with the module's own authority instead.
const delegationErrorDomain = "accounts.saas.codefly.dev"

var (
	// ErrDelegationMissing: the source has no active delegation to this
	// module. A person must connect or reconnect the source to authorize its
	// work.
	ErrDelegationMissing = errors.New("module authority: the source has no active delegation to this module")
	// ErrDelegationRevoked: the delegation was revoked, explicitly or because
	// the person who made it left, lost the permission, or the source was
	// deleted.
	ErrDelegationRevoked = errors.New("module authority: the source's delegation was revoked")
	// ErrDelegationInvalid: no delegation this module may use answers to the
	// request.
	ErrDelegationInvalid = errors.New("module authority: the delegation is not one this module may use")
)

// SourceOperationContext is a Work Context minted through a source's
// delegation: it acts in the source's organization, is owned by the person who
// connected the source, and names this module as the sole actor.
type SourceOperationContext struct {
	OperationContext
	// OwnerPrincipalID is the person the delegation acts for.
	OwnerPrincipalID string
	DelegationID     string
	SourceID         string
}

// MintSourceOperationContext obtains a short-lived Work Context through the
// active delegation of one datasource source to this module. SaaS resolves the
// delegation from the source and the module's proven identity, derives the
// organization, owner, audience and scopes from it, and re-checks the
// delegation and the person's current membership and permission on every
// mint. The caller names only the source; it can never pick an organization.
//
// A refusal is ErrDelegationMissing, ErrDelegationRevoked or
// ErrDelegationInvalid (wrapped with the SaaS error); anything else is an
// ordinary failure the caller may retry. The result is never cached.
func (c *Client) MintSourceOperationContext(ctx context.Context, sourceID string) (SourceOperationContext, error) {
	if sourceID == "" {
		return SourceOperationContext{}, ErrInvalidExchange
	}
	if c.credentials.Prefix == "" || c.credentials.Secret == "" {
		return SourceOperationContext{}, ErrInvalidCredentials
	}
	resp, err := c.inner.MintSourceOperationContext(ctx, connect.NewRequest(&v1.ModuleMintSourceOperationContextRequest{
		Prefix:     c.credentials.Prefix,
		Secret:     c.credentials.Secret,
		Delegation: &v1.ModuleMintSourceOperationContextRequest_SourceId{SourceId: sourceID},
	}))
	if err != nil {
		if refusal := delegationRefusal(err); refusal != nil {
			return SourceOperationContext{}, fmt.Errorf("%w: %w", refusal, err)
		}
		return SourceOperationContext{}, fmt.Errorf("module authority: mint source operation context: %w", err)
	}
	msg := resp.Msg
	if msg.GetExpiresAt() == nil {
		return SourceOperationContext{}, fmt.Errorf("%w: missing expiry", ErrInvalidCapability)
	}
	expiresAt := msg.GetExpiresAt().AsTime()
	if !expiresAt.After(c.now()) {
		return SourceOperationContext{}, fmt.Errorf("%w: capability is already expired", ErrInvalidCapability)
	}
	if msg.GetSourceId() != sourceID {
		return SourceOperationContext{}, fmt.Errorf("%w: issued for another source", ErrInvalidCapability)
	}
	if msg.GetTenant() == "" || msg.GetOwnerPrincipalId() == "" || msg.GetDelegationId() == "" {
		return SourceOperationContext{}, fmt.Errorf("%w: missing tenant, owner or delegation", ErrInvalidCapability)
	}
	token, err := codefly.ParseWorkContextToken(msg.GetToken())
	if err != nil {
		return SourceOperationContext{}, fmt.Errorf("%w: malformed operation token", ErrInvalidCapability)
	}
	return SourceOperationContext{
		OperationContext: OperationContext{
			Token:       token,
			ExpiresAt:   expiresAt,
			PrincipalID: msg.GetPrincipalId(),
			Tenant:      msg.GetTenant(),
			Audience:    msg.GetAudience(),
			BindingID:   msg.GetBinding(),
		},
		OwnerPrincipalID: msg.GetOwnerPrincipalId(),
		DelegationID:     msg.GetDelegationId(),
		SourceID:         msg.GetSourceId(),
	}, nil
}

// delegationRefusal maps SaaS's typed delegation refusal to its sentinel, or
// nil when the error is not one. Only the code and ErrorInfo together
// qualify: a bare PermissionDenied without the reason is not claimed to be a
// delegation refusal.
func delegationRefusal(err error) error {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return nil
	}
	for _, detail := range ce.Details() {
		value, derr := detail.Value()
		if derr != nil {
			continue
		}
		info, ok := value.(*errdetails.ErrorInfo)
		if !ok || info.GetDomain() != delegationErrorDomain {
			continue
		}
		switch {
		case ce.Code() == connect.CodeFailedPrecondition && info.GetReason() == "DELEGATION_MISSING":
			return ErrDelegationMissing
		case ce.Code() == connect.CodePermissionDenied && info.GetReason() == "DELEGATION_REVOKED":
			return ErrDelegationRevoked
		case ce.Code() == connect.CodePermissionDenied && info.GetReason() == "DELEGATION_INVALID":
			return ErrDelegationInvalid
		}
	}
	return nil
}
