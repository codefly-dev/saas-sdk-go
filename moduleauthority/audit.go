package moduleauthority

import (
	"context"
	"fmt"
	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// DeclareAuditEventTypes admits this module's event schemas through the same
// authenticated authority seam as emission. The host validates ownership and
// compatibility; the SDK neither invents a namespace nor widens the declaration.
func (c *Client) DeclareAuditEventTypes(ctx context.Context, types []*ModuleAuditEventTypeDeclaration) error {
	_, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.DeclareAuditEventTypes,
		&v1.ModuleDeclareAuditEventTypesRequest{Prefix: c.credentials.Prefix, Types: types})
	if err != nil {
		return fmt.Errorf("module authority: declare audit event types: %w", err)
	}
	return nil
}
