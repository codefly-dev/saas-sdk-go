package moduleauthority

import (
	"context"
	"errors"
	"fmt"
	"strings"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
	codefly "github.com/codefly-dev/sdk-go/workcontext"
)

var ErrInvalidRuntimeBoundary = errors.New("module authority: invalid runtime boundary attestation")

// RuntimeBoundary is a live host observation of a signed context. It is not a
// grant or durable liveness proof. Consumers must match both fields against
// their independently verified incoming context and authorize its scopes.
type RuntimeBoundary struct {
	TenantID   string
	BoundaryID string
}

// VerifyWorkContextRuntimeBoundary forwards the original viewer capability in
// the request while authenticating this module independently. It neither mints
// a viewer context nor supplies a caller-chosen tenant or boundary selector.
func (c *Client) VerifyWorkContextRuntimeBoundary(ctx context.Context, forwarded codefly.WorkContextToken) (RuntimeBoundary, error) {
	if forwarded.Encoded() == "" {
		return RuntimeBoundary{}, ErrInvalidRuntimeBoundary
	}
	out, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.VerifyWorkContextRuntimeBoundary, &v1.VerifyWorkContextRuntimeBoundaryRequest{ForwardedWorkContextToken: forwarded.Encoded()})
	if err != nil {
		return RuntimeBoundary{}, fmt.Errorf("module authority: runtime boundary: %w", err)
	}
	if out == nil || strings.TrimSpace(out.TenantId) == "" || strings.TrimSpace(out.BoundaryId) == "" {
		return RuntimeBoundary{}, ErrInvalidRuntimeBoundary
	}
	return RuntimeBoundary{TenantID: out.TenantId, BoundaryID: out.BoundaryId}, nil
}
