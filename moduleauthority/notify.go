package moduleauthority

import (
	"context"
	"errors"
	"fmt"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// ErrInvalidNotice is returned before any call for a notice without a tenant,
// title or category.
var ErrInvalidNotice = errors.New("module authority: invalid notice")

// OrgAdminsNotice is a notification for the administrators of one tenant. There
// is deliberately no recipient: the host resolves the tenant's admin and owner
// members when the call is made, and never tells the module who they are.
type OrgAdminsNotice struct {
	// Tenant is the organization whose administrators are notified; it must be
	// the tenant this module is bound to (or the module must be cross-tenant).
	Tenant string
	Title  string
	Body   string
	// Type is one of info, success, warning, error, billing, security; empty
	// means info. The host refuses any other as InvalidArgument.
	Type      string
	ActionURL string
	// Category is the notification policy category (security, billing,
	// product, marketing, digest); the recipients' settings apply to an
	// optional one.
	Category string
	// IdempotencyKey dedupes a redelivered notice per recipient: the same notice
	// under the same key converges on the rows already written, and a different
	// notice under a used key is FailedPrecondition.
	IdempotencyKey string
}

// NotifyOrgAdmins notifies a tenant's administrators on the authority endpoint,
// as this module. It reports only whether anyone received the notice — false
// when the tenant has no administrator or category policy suppressed it for
// every one. A rejected module capability is refreshed and retried once, like
// every authority call.
func (c *Client) NotifyOrgAdmins(ctx context.Context, notice OrgAdminsNotice) (bool, error) {
	if notice.Tenant == "" || notice.Title == "" || notice.Category == "" {
		return false, ErrInvalidNotice
	}
	res, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.NotifyOrgAdmins, &v1.ModuleNotifyOrgAdminsRequest{
		Tenant:         notice.Tenant,
		Title:          notice.Title,
		Body:           notice.Body,
		Type:           notice.Type,
		ActionUrl:      notice.ActionURL,
		Category:       notice.Category,
		IdempotencyKey: notice.IdempotencyKey,
	})
	if err != nil {
		return false, fmt.Errorf("module authority: notify org admins: %w", err)
	}
	return res.GetDelivered(), nil
}
