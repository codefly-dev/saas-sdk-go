package accounts

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// Organizations returns the typed OrganizationService facade.
func (c *Client) Organizations() *OrganizationsClient {
	return &OrganizationsClient{
		inner: accountsv1connect.NewOrganizationServiceClient(c.gw.HTTPClient(), c.gw.BaseURL(), c.opts...),
	}
}

// OrganizationsClient wraps the generated OrganizationServiceClient and hides
// the connect.Request/Response envelope.
type OrganizationsClient struct {
	inner accountsv1connect.OrganizationServiceClient
}

// ListOrganizations returns the organizations the caller belongs to. The call
// is scoped by the bearer the gateway forwards; it takes no argument.
func (o *OrganizationsClient) ListOrganizations(ctx context.Context) ([]*Organization, error) {
	resp, err := o.inner.ListOrganizations(ctx, connect.NewRequest(&ListOrganizationsRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetOrganizations(), nil
}

// AccessibleScopes returns the typed AccessibleScopeService facade: the
// caller-scoped view of which scope nodes the signed-in person may act on.
func (c *Client) AccessibleScopes() *AccessibleScopesClient {
	return &AccessibleScopesClient{
		inner: accountsv1connect.NewAccessibleScopeServiceClient(c.gw.HTTPClient(), c.gw.BaseURL(), c.opts...),
	}
}

// AccessibleScopesClient wraps the generated AccessibleScopeServiceClient.
type AccessibleScopesClient struct {
	inner accountsv1connect.AccessibleScopeServiceClient
}

// ListMyAccessibleScopes returns one page of the caller's accessible scopes.
func (a *AccessibleScopesClient) ListMyAccessibleScopes(ctx context.Context, req *ListMyAccessibleScopesRequest) (*ListAccessibleScopesResponse, error) {
	resp, err := a.inner.ListMyAccessibleScopes(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// MaxAccessibleScopePages bounds ListAll: a caller never follows more pages
// than this, whatever the server returns.
const MaxAccessibleScopePages = 1000

// accessibleScopePageSize is the server's page ceiling, so a drain costs as
// few round trips as the contract allows.
const accessibleScopePageSize = 1000

// ErrAccessibleScopePagination reports a scope listing that could not be
// drained: its cursor repeated, or it ran past MaxAccessibleScopePages. The
// partial set is never returned — treating a prefix of the grants as all of
// them silently hides scopes the caller holds.
var ErrAccessibleScopePagination = errors.New("accounts: accessible scope pagination did not complete")

// ListAll drains every page of the caller's scopes for one resource type and
// action in orgID, following next_page_token to the end. An empty result is a
// resolved answer — the caller is granted nothing — not a failure. Any error,
// including a denial, fails the whole listing: a partial grant set is never
// returned in place of the full one.
func (a *AccessibleScopesClient) ListAll(ctx context.Context, orgID, resourceType, action string) ([]*AccessibleScope, error) {
	all := []*AccessibleScope{}
	seen := map[string]bool{}
	token := ""
	for page := 0; ; page++ {
		if page >= MaxAccessibleScopePages {
			return nil, fmt.Errorf("%w: exceeds the %d-page bound", ErrAccessibleScopePagination, MaxAccessibleScopePages)
		}
		resp, err := a.ListMyAccessibleScopes(ctx, &ListMyAccessibleScopesRequest{
			OrgId:        orgID,
			ResourceType: resourceType,
			Action:       action,
			PageSize:     accessibleScopePageSize,
			PageToken:    token,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, resp.GetScopes()...)
		next := resp.GetNextPageToken()
		if next == "" {
			return all, nil
		}
		if seen[next] {
			return nil, fmt.Errorf("%w: page token %q repeated", ErrAccessibleScopePagination, next)
		}
		seen[next] = true
		token = next
	}
}

// WorkContexts returns the typed WorkContextService facade.
func (c *Client) WorkContexts() *WorkContextsClient {
	return &WorkContextsClient{
		inner: accountsv1connect.NewWorkContextServiceClient(c.gw.HTTPClient(), c.gw.BaseURL(), c.opts...),
	}
}

// WorkContextsClient wraps the generated WorkContextServiceClient.
type WorkContextsClient struct {
	inner accountsv1connect.WorkContextServiceClient
}

// StartTask asks accounts to issue a signed Work Context for a new Task. The
// issuance names the owner and current actor accounts resolved from the
// forwarded bearer, so a caller can compare them against an identity it was
// handed instead of trusting that identity.
func (w *WorkContextsClient) StartTask(ctx context.Context, req *StartTaskWorkContextRequest) (*IssuedWorkContext, error) {
	resp, err := w.inner.StartTask(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
