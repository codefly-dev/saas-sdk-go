package moduleauthority

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// The delegation refusals the broker's POST /modules/_source-operation-context
// names in its plain-text body. Each is a denial a caller must surface, never
// an outage to retry, and none of them may be answered by acting with the
// module's own authority instead.
const (
	delegationMissingReason = "DELEGATION_MISSING" // with HTTP 412
	delegationRevokedReason = "DELEGATION_REVOKED" // with HTTP 403
	delegationInvalidReason = "DELEGATION_INVALID" // with HTTP 403
)

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

// sourceOperationContextResponse is POST /modules/_source-operation-context's
// answer: the operation mint's shape plus the delegation it came from.
type sourceOperationContextResponse struct {
	operationContextResponse
	OwnerPrincipalID string `json:"owner_principal_id"`
	DelegationID     string `json:"delegation_id"`
	SourceID         string `json:"source_id"`
}

// MintSourceOperationContext obtains a short-lived Work Context through the
// active delegation of one datasource source to this module, at the gateway
// broker (POST /modules/_source-operation-context). SaaS resolves the
// delegation from the source and the module's proven identity, derives the
// organization, owner, audience and scopes from it, and re-checks the
// delegation and the person's current membership and permission on every
// mint. The caller names only the source; it can never pick an organization.
//
// A refusal is typed, and wraps the *BrokerError:
//   - HTTP 412 DELEGATION_MISSING is ErrDelegationMissing;
//   - HTTP 403 DELEGATION_REVOKED is ErrDelegationRevoked;
//   - any other HTTP 403 is ErrDelegationInvalid — DELEGATION_INVALID, and
//     also a 403 whose body names no reason (a host that predates the
//     reasons), since every 403 of this mint is a delegation the module may
//     not use;
//   - HTTP 401 is ErrInvalidCredentials.
//
// Anything else (400, 502, transport errors) is an ordinary failure; a 502 may
// be retried. The result is never cached.
func (c *Client) MintSourceOperationContext(ctx context.Context, sourceID string) (SourceOperationContext, error) {
	if sourceID == "" {
		return SourceOperationContext{}, ErrInvalidExchange
	}
	var issued sourceOperationContextResponse
	if err := c.broker.post(ctx, sourceOperationContextPath, c.credentials.Secret, map[string]string{
		"prefix":    c.credentials.Prefix,
		"source_id": sourceID,
	}, &issued); err != nil {
		if refusal := delegationRefusal(err); refusal != nil {
			return SourceOperationContext{}, fmt.Errorf("%w: %w", refusal, err)
		}
		return SourceOperationContext{}, fmt.Errorf("module authority: mint source operation context: %w", err)
	}
	op, err := issued.operationContext(c.now())
	if err != nil {
		return SourceOperationContext{}, err
	}
	if issued.SourceID != sourceID {
		return SourceOperationContext{}, fmt.Errorf("%w: issued for another source", ErrInvalidCapability)
	}
	if op.Tenant == "" || issued.OwnerPrincipalID == "" || issued.DelegationID == "" {
		return SourceOperationContext{}, fmt.Errorf("%w: missing tenant, owner or delegation", ErrInvalidCapability)
	}
	return SourceOperationContext{
		OperationContext: op,
		OwnerPrincipalID: issued.OwnerPrincipalID,
		DelegationID:     issued.DelegationID,
		SourceID:         issued.SourceID,
	}, nil
}

// delegationRefusal maps the broker's refusal of a source mint to its
// sentinel, or nil when the error is not one. The status decides first; the
// body refines a 403. A 412 is claimed only with its reason: the broker sends
// no other.
func delegationRefusal(err error) error {
	var be *BrokerError
	if !errors.As(err, &be) {
		return nil
	}
	switch be.StatusCode {
	case http.StatusUnauthorized:
		return ErrInvalidCredentials
	case http.StatusPreconditionFailed:
		if be.Reason == delegationMissingReason {
			return ErrDelegationMissing
		}
	case http.StatusForbidden:
		if be.Reason == delegationRevokedReason {
			return ErrDelegationRevoked
		}
		return ErrDelegationInvalid
	}
	return nil
}
