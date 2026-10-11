package moduleauthority

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
	codefly "github.com/codefly-dev/sdk-go/workcontext"
)

var ErrInvalidInstallation = errors.New("module authority: invalid current installation request or response")

// CurrentInstallationRequest selects one installation. The host derives the
// tenant, owner and actors only from Parent, verified independently of the module.
type CurrentInstallationRequest struct {
	Parent         codefly.WorkContextToken
	InstallationID string
}

// CurrentInstallation is a current active, non-revoked identity observation.
// It is not executable approval, a grant, or a durable proof of liveness.
type CurrentInstallation struct {
	InstallationID string
	TenantID       string
	TargetID       string
	BindingID      string
}

// GetCurrentInstallation preserves the host's organization-member metadata read.
// It adds no permission, binding, viewer bearer or authority mint for the parent.
// Callers must match TenantID to their independently verified request tenant.
func (c *Client) GetCurrentInstallation(ctx context.Context, req CurrentInstallationRequest) (CurrentInstallation, error) {
	if req.Parent.Encoded() == "" || !artifactUUID.MatchString(req.InstallationID) {
		return CurrentInstallation{}, ErrInvalidInstallation
	}
	out, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.GetCurrentInstallation, &v1.ModuleCurrentInstallationRequest{ParentWorkContextToken: req.Parent.Encoded(), InstallationId: req.InstallationID})
	if err != nil {
		return CurrentInstallation{}, fmt.Errorf("module authority: current installation: %w", err)
	}
	if out == nil || out.InstallationId != req.InstallationID || !artifactUUID.MatchString(out.TenantId) || !artifactUUID.MatchString(out.TargetId) || strings.TrimSpace(out.BindingId) == "" || !utf8.ValidString(out.BindingId) {
		return CurrentInstallation{}, ErrInvalidInstallation
	}
	return CurrentInstallation{InstallationID: out.InstallationId, TenantID: out.TenantId, TargetID: out.TargetId, BindingID: out.BindingId}, nil
}
