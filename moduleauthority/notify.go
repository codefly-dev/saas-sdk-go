package moduleauthority

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// ErrInvalidNotice is returned before any call for a notice without a tenant,
// title or category, or a UserNotice without a recipient.
var ErrInvalidNotice = errors.New("module authority: invalid notice")

// ErrTenantMembershipMissing identifies a definitive refusal for this recipient.
var ErrTenantMembershipMissing = errors.New("recipient is not a tenant member")

// ErrNotificationIdempotencyConflict identifies a reused key with different content.
var ErrNotificationIdempotencyConflict = errors.New("notification idempotency conflict")

// ErrLiveAuthorityUnavailable identifies a host unable to check current authority.
var ErrLiveAuthorityUnavailable = errors.New("live authority unavailable")

// NotificationError classifies Accounts notification refusals from a raw generated
// client. The facade applies it automatically. Unknown failures pass through,
// and every classified error still unwraps to the original transport error.
func NotificationError(err error) error {
	var rpc *connect.Error
	if !errors.As(err, &rpc) {
		return err
	}
	for _, detail := range rpc.Details() {
		value, decodeErr := detail.Value()
		info, ok := value.(*errdetails.ErrorInfo)
		if decodeErr != nil || !ok || info.Domain != "saas.accounts.v1" {
			continue
		}
		switch {
		case rpc.Code() == connect.CodeFailedPrecondition && info.Reason == "TENANT_MEMBERSHIP_MISSING":
			return fmt.Errorf("%w: %w", ErrTenantMembershipMissing, err)
		case rpc.Code() == connect.CodeFailedPrecondition && info.Reason == "NOTIFICATION_IDEMPOTENCY_CONFLICT":
			return fmt.Errorf("%w: %w", ErrNotificationIdempotencyConflict, err)
		case rpc.Code() == connect.CodeUnavailable && info.Reason == "LIVE_AUTHORITY_UNAVAILABLE":
			return fmt.Errorf("%w: %w", ErrLiveAuthorityUnavailable, err)
		}
	}
	return err
}

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
		return false, fmt.Errorf("module authority: notify org admins: %w", NotificationError(err))
	}
	return res.GetDelivered(), nil
}

// UserNotice is a notification for one person in one tenant. The module names
// the recipient; the host checks that they are a member of the tenant before it
// writes anything, and routes the notice through their category policy.
type UserNotice struct {
	// Tenant is the organization the notice belongs to; it must be the tenant
	// this module is bound to (or the module must be cross-tenant).
	Tenant string
	// UserID is the recipient. A definitive host refusal for a departed member
	// matches ErrTenantMembershipMissing through errors.Is.
	UserID string
	Title  string
	Body   string
	// Type is one of info, success, warning, error, billing, security; empty
	// means info. The host refuses any other as InvalidArgument.
	Type string
	// ActionURL is the link the host shows with the notice. A module should
	// send a same-origin relative path: codefly-dev/module-saas-starter#1012
	// asks the host to refuse anything else from a module.
	ActionURL string
	// Category is the notification policy category (security, billing,
	// product, marketing, digest); the recipient's settings apply to an
	// optional one.
	Category string
	// IdempotencyKey dedupes a redelivered notice: the same notice under the
	// same key converges on the row already written, and a different notice
	// under a used key is FailedPrecondition.
	IdempotencyKey string
}

// NotifyUser notifies one person on the authority endpoint, as this module. It
// reports whether the notice was delivered — false when the recipient's
// settings suppressed an optional category, which is not an error. A rejected
// module capability is refreshed and retried once, like every authority call.
func (c *Client) NotifyUser(ctx context.Context, notice UserNotice) (bool, error) {
	if notice.Tenant == "" || notice.UserID == "" || notice.Title == "" || notice.Category == "" {
		return false, ErrInvalidNotice
	}
	res, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.NotifyUser, &v1.ModuleNotifyUserRequest{
		Tenant:         notice.Tenant,
		UserId:         notice.UserID,
		Title:          notice.Title,
		Body:           notice.Body,
		Type:           notice.Type,
		ActionUrl:      notice.ActionURL,
		Category:       notice.Category,
		IdempotencyKey: notice.IdempotencyKey,
	})
	if err != nil {
		return false, fmt.Errorf("module authority: notify user: %w", NotificationError(err))
	}
	return res.GetDelivered(), nil
}
