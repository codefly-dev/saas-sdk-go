package moduleauthority

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

var (
	// ErrDelegationMissing: the source has no active delegation to this
	// module. A person must connect or reconnect the source to authorize its
	// work.
	//
	// Deprecated: broker response bodies are excluded from errors. Source
	// mints now return a BrokerError with status 412 without this sentinel.
	ErrDelegationMissing = errors.New("module authority: the source has no active delegation to this module")
	// ErrDelegationRevoked: the delegation was revoked, explicitly or because
	// the person who made it left, lost the permission, or the source was
	// deleted.
	//
	// Deprecated: broker response bodies are excluded from errors. Every
	// source-mint 403 now returns ErrDelegationInvalid instead.
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
//   - HTTP 403 is ErrDelegationInvalid, since every 403 of this mint is a
//     delegation the module may not use;
//   - HTTP 401 is ErrInvalidCredentials;
//   - HTTP 503 is ErrExecutionUnestablished, and may be retried.
//
// Other non-200 statuses (including 412) carry only BrokerError. Bodies are
// never read to distinguish missing or revoked delegations. Transport and read
// failures carry BrokerTransportError. A 502 may be retried. The result is never
// cached, and a refusal must never be answered with the module's own authority.
func (c *Client) MintSourceOperationContext(ctx context.Context, sourceID string) (SourceOperationContext, error) {
	if sourceID == "" {
		return SourceOperationContext{}, ErrInvalidExchange
	}
	projected, err := c.credentials.projectedToken()
	if err != nil {
		return SourceOperationContext{}, err
	}
	var issued sourceOperationContextResponse
	if err := c.broker.post(ctx, sourceOperationContextPath, projected, map[string]string{
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
// sentinel, or nil when the error is not one. Only the status is considered.
func delegationRefusal(err error) error {
	var be *BrokerError
	if !errors.As(err, &be) {
		return nil
	}
	switch be.StatusCode {
	case http.StatusUnauthorized:
		return ErrInvalidCredentials
	case http.StatusServiceUnavailable:
		return ErrExecutionUnestablished
	case http.StatusForbidden:
		return ErrDelegationInvalid
	}
	return nil
}
