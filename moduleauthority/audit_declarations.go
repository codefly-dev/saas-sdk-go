package moduleauthority

import (
	"context"
	"fmt"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// DeclareAuditEventTypes presents this module's event schemas through the
// client's selected authority seam, using its own prefix and cached module
// capability. The host validates ownership and compatibility; the SDK forwards
// the complete declaration without changing it. An identical re-declaration is
// idempotent at the host, so modules may declare on startup.
//
// Like other authority calls, it refreshes an Unauthenticated module capability
// and retries once. Other refusals retain their Connect codes and details and
// are not retried. A mint-only client returns ErrNoAuthority before any request.
func (c *Client) DeclareAuditEventTypes(ctx context.Context, types []*ModuleAuditEventTypeDeclaration) error {
	_, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.DeclareAuditEventTypes,
		&v1.ModuleDeclareAuditEventTypesRequest{Prefix: c.credentials.Prefix, Types: types})
	if err != nil {
		return fmt.Errorf("module authority: declare audit event types: %w", err)
	}
	return nil
}
