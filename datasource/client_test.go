package datasource_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	"github.com/codefly-dev/saas-sdk-go/datasource"
	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// recordingHandler is a real DatasourceService Connect server that captures the
// request each RPC received and returns a canned response, so the tests exercise
// the actual generated stubs end to end rather than a mock of them.
type recordingHandler struct {
	accountsv1connect.UnimplementedDatasourceServiceHandler
	addReq  *v1.AddGitHubSourceRequest
	listReq *v1.ListSourcesRequest
	syncReq *v1.SyncSourceRequest
}

func (h *recordingHandler) AddGitHubSource(_ context.Context, req *connect.Request[v1.AddGitHubSourceRequest]) (*connect.Response[v1.AddGitHubSourceResponse], error) {
	h.addReq = req.Msg
	return connect.NewResponse(&v1.AddGitHubSourceResponse{
		Datasource: &v1.Datasource{Id: "ds-1", OrgId: req.Msg.GetOrgId(), BoundaryNodeId: "boundary-1"},
	}), nil
}

func (h *recordingHandler) ListSources(_ context.Context, req *connect.Request[v1.ListSourcesRequest]) (*connect.Response[v1.ListSourcesResponse], error) {
	h.listReq = req.Msg
	return connect.NewResponse(&v1.ListSourcesResponse{
		Datasources: []*v1.Datasource{{Id: "ds-1"}, {Id: "ds-2"}},
	}), nil
}

func (h *recordingHandler) SyncSource(_ context.Context, req *connect.Request[v1.SyncSourceRequest]) (*connect.Response[v1.SyncSourceResponse], error) {
	h.syncReq = req.Msg
	return connect.NewResponse(&v1.SyncSourceResponse{JobId: "job-42"}), nil
}

// gw satisfies datasource.Gateway against an httptest server.
type gw struct {
	base   string
	client *http.Client
}

func (g gw) BaseURL() string          { return g.base }
func (g gw) HTTPClient() *http.Client { return g.client }

func newClient(t *testing.T) (*datasource.Client, *recordingHandler) {
	t.Helper()
	h := &recordingHandler{}
	mux := http.NewServeMux()
	mux.Handle(accountsv1connect.NewDatasourceServiceHandler(h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return datasource.New(gw{base: srv.URL, client: srv.Client()}), h
}

func TestAddGitHubSourceMapsFieldsAndUnwraps(t *testing.T) {
	c, h := newClient(t)

	ds, err := c.AddGitHubSource(context.Background(), datasource.GitHubSource{
		OrgID:          "11111111-1111-1111-1111-111111111111",
		Repo:           "codefly-dev/module-saas-starter",
		Paths:          []string{"docs", "handbook"},
		Branch:         "main",
		Collection:     "handbook",
		FileExtensions: []string{".md", ".mdx"},
		AccessToken:    "ghp_secret",
		WebhookSecret:  "whsec",
	})
	if err != nil {
		t.Fatalf("AddGitHubSource: %v", err)
	}

	// The ergonomic struct fields land on the right proto fields.
	if got := h.addReq.GetRepo(); got != "codefly-dev/module-saas-starter" {
		t.Errorf("repo = %q", got)
	}
	if got := h.addReq.GetCollectionLabel(); got != "handbook" {
		t.Errorf("collection_label = %q, want handbook", got)
	}
	if got := h.addReq.GetAccessToken(); got != "ghp_secret" {
		t.Errorf("access_token = %q", got)
	}
	if got := h.addReq.GetWebhookSecret(); got != "whsec" {
		t.Errorf("webhook_secret = %q", got)
	}
	if got := h.addReq.GetPaths(); len(got) != 2 || got[0] != "docs" || got[1] != "handbook" {
		t.Errorf("paths = %v", got)
	}
	if got := h.addReq.GetFileExtensions(); len(got) != 2 || got[0] != ".md" || got[1] != ".mdx" {
		t.Errorf("file_extensions = %v", got)
	}
	// The envelope is unwrapped to the bare Datasource.
	if ds.GetId() != "ds-1" || ds.GetBoundaryNodeId() != "boundary-1" {
		t.Errorf("datasource = %+v", ds)
	}
}

func TestAddGitHubSourceUsesExistingBoundary(t *testing.T) {
	c, h := newClient(t)
	_, err := c.AddGitHubSource(context.Background(), datasource.GitHubSource{
		OrgID:          "org-1",
		Repo:           "owner/repo",
		BoundaryNodeID: "boundary-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.addReq.GetBoundaryNodeId(); got != "boundary-1" {
		t.Fatalf("boundary_node_id = %q", got)
	}
}

func TestAddGitHubSourceRequiresExactlyOneBoundary(t *testing.T) {
	c, h := newClient(t)
	for _, src := range []datasource.GitHubSource{
		{},
		{Collection: "collection", BoundaryNodeID: "boundary-1"},
	} {
		if _, err := c.AddGitHubSource(context.Background(), src); err != datasource.ErrInvalidBoundary {
			t.Fatalf("error = %v, want %v", err, datasource.ErrInvalidBoundary)
		}
	}
	if h.addReq != nil {
		t.Fatal("invalid source reached the service")
	}
}

func TestListSourcesReturnsBareSlice(t *testing.T) {
	c, h := newClient(t)

	got, err := c.ListSources(context.Background(), "org-1")
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	if h.listReq.GetOrgId() != "org-1" {
		t.Errorf("org_id = %q", h.listReq.GetOrgId())
	}
	if len(got) != 2 {
		t.Fatalf("len(datasources) = %d, want 2", len(got))
	}
}

func TestSyncReturnsJobID(t *testing.T) {
	c, h := newClient(t)

	jobID, err := c.Sync(context.Background(), "org-1", "ds-1")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if h.syncReq.GetOrgId() != "org-1" || h.syncReq.GetId() != "ds-1" {
		t.Errorf("req = %+v", h.syncReq)
	}
	if jobID != "job-42" {
		t.Errorf("job_id = %q, want job-42", jobID)
	}
}
