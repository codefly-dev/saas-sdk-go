// Package datasource is the typed, gateway-bound client facade for the saas
// DatasourceService — the "connection" half of datasource ingestion. It is the
// syntactic-sugar layer over the generated Connect stubs, mirroring the accounts
// facade: it binds the generated client to a solution-runtime Gateway and
// unwraps the connect.Request/Response envelope so a solution declares a source
// in a few lines:
//
//	ds := datasource.New(gw)
//	src, err := ds.AddGitHubSource(ctx, datasource.GitHubSource{
//		OrgID:       org,
//		Repo:        "codefly-dev/module-saas-starter",
//		Paths:       []string{"docs"},
//		Collection:  "handbook",
//		AccessToken: token,
//	})
//	_, err = ds.Sync(ctx, org, src.GetId())
//
// The access token is sent once; the connection side encrypts it through the
// SecretCipher and persists only a secret reference — no read ever returns it.
package datasource

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

var ErrInvalidBoundary = errors.New("datasource: exactly one collection label or boundary node ID is required")

// Gateway is the minimal surface this SDK needs from the solution runtime.
// *github.com/codefly-dev/solution-runtime-go.Gateway satisfies it as-is
// (BaseURL() string, HTTPClient() *http.Client), so the generic runtime stays
// datasource-agnostic and takes no dependency on this package.
type Gateway interface {
	BaseURL() string
	HTTPClient() *http.Client
}

// Client is the entry point: datasource.New(gw).AddGitHubSource(...).
type Client struct {
	inner accountsv1connect.DatasourceServiceClient
}

// New binds the datasource SDK to a gateway. Extra connect.ClientOptions (e.g.
// connect.WithGRPC()) are forwarded to the underlying client.
func New(gw Gateway, opts ...connect.ClientOption) *Client {
	return &Client{
		inner: accountsv1connect.NewDatasourceServiceClient(gw.HTTPClient(), gw.BaseURL(), opts...),
	}
}

// GitHubSource is the ergonomic argument to AddGitHubSource. It carries only the
// fields a solution supplies at creation; the server assigns id/status/timestamps
// and returns them on the resulting Datasource.
type GitHubSource struct {
	// OrgID is the owning organization's uuid.
	OrgID string
	// Repo is the "owner/name" GitHub repository slug.
	Repo string
	// Paths restricts ingestion to these subtrees; empty means the whole repo.
	Paths []string
	// Branch is the git ref to pull; empty resolves to the default branch.
	Branch string
	// Collection is the documents-store collection the pulled Entries land in.
	// It is resolved to a new collection boundary by the host. Set exactly one
	// of Collection or BoundaryNodeID.
	Collection string
	// BoundaryNodeID selects an existing scope boundary for ingestion. Set
	// exactly one of BoundaryNodeID or Collection.
	BoundaryNodeID string
	// FileExtensions is an optional suffix allowlist (for example, [".md"]).
	// The host intersects it with Paths.
	FileExtensions []string
	// AccessToken is the plaintext GitHub token used to read the repository. It
	// is encrypted at receipt and stored only as a secret reference.
	AccessToken string
	// WebhookSecret is the shared secret GitHub signs push deliveries with.
	// Optional; when empty, live webhook ingestion stays off until supplied.
	WebhookSecret string
}

// AddGitHubSource registers a GitHub repository as a datasource and returns the
// non-secret projection the server stored.
func (c *Client) AddGitHubSource(ctx context.Context, src GitHubSource) (*Datasource, error) {
	if (src.Collection == "") == (src.BoundaryNodeID == "") {
		return nil, ErrInvalidBoundary
	}
	req := &v1.AddGitHubSourceRequest{
		OrgId:          src.OrgID,
		Repo:           src.Repo,
		Paths:          src.Paths,
		Branch:         src.Branch,
		AccessToken:    src.AccessToken,
		WebhookSecret:  src.WebhookSecret,
		FileExtensions: src.FileExtensions,
	}
	if src.BoundaryNodeID != "" {
		req.Boundary = &v1.AddGitHubSourceRequest_BoundaryNodeId{BoundaryNodeId: src.BoundaryNodeID}
	} else {
		req.Boundary = &v1.AddGitHubSourceRequest_CollectionLabel{CollectionLabel: src.Collection}
	}
	resp, err := c.inner.AddGitHubSource(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetDatasource(), nil
}

// ListSources returns the org's connected datasources.
func (c *Client) ListSources(ctx context.Context, orgID string) ([]*Datasource, error) {
	resp, err := c.inner.ListSources(ctx, connect.NewRequest(&v1.ListSourcesRequest{OrgId: orgID}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetDatasources(), nil
}

// Sync marks a source for ingestion and returns the durable jobs-inbox id of
// the enqueued request.
func (c *Client) Sync(ctx context.Context, orgID, id string) (string, error) {
	resp, err := c.inner.SyncSource(ctx, connect.NewRequest(&v1.SyncSourceRequest{OrgId: orgID, Id: id}))
	if err != nil {
		return "", err
	}
	return resp.Msg.GetJobId(), nil
}
