package accounts_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	"github.com/codefly-dev/saas-sdk-go/accounts"
	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// gw satisfies accounts.Gateway against an httptest server.
type gw struct {
	base   string
	client *http.Client
}

func (g gw) BaseURL() string          { return g.base }
func (g gw) HTTPClient() *http.Client { return g.client }

// serve runs real generated Connect handlers, so these tests exercise the
// generated stubs end to end rather than a mock of them.
func serve(t *testing.T, register func(*http.ServeMux)) *accounts.Client {
	t.Helper()
	mux := http.NewServeMux()
	register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return accounts.New(gw{base: srv.URL, client: srv.Client()})
}

type orgs struct {
	accountsv1connect.UnimplementedOrganizationServiceHandler
}

func (orgs) ListOrganizations(context.Context, *connect.Request[v1.ListOrganizationsRequest]) (*connect.Response[v1.ListOrganizationsResponse], error) {
	return connect.NewResponse(&v1.ListOrganizationsResponse{Organizations: []*v1.Organization{{Id: "org-1"}, {Id: "org-2"}}}), nil
}

func TestListOrganizationsUnwraps(t *testing.T) {
	c := serve(t, func(mux *http.ServeMux) { mux.Handle(accountsv1connect.NewOrganizationServiceHandler(orgs{})) })
	got, err := c.Organizations().ListOrganizations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].GetId() != "org-1" {
		t.Fatalf("organizations = %v", got)
	}
}

// scopes answers pages from a fixed script keyed by the request's page token.
type scopes struct {
	accountsv1connect.UnimplementedAccessibleScopeServiceHandler
	pages    map[string]*v1.ListAccessibleScopesResponse
	requests []*v1.ListMyAccessibleScopesRequest
	fail     error
}

func (s *scopes) ListMyAccessibleScopes(_ context.Context, req *connect.Request[v1.ListMyAccessibleScopesRequest]) (*connect.Response[v1.ListAccessibleScopesResponse], error) {
	s.requests = append(s.requests, req.Msg)
	if s.fail != nil && req.Msg.GetPageToken() != "" {
		return nil, s.fail
	}
	page, ok := s.pages[req.Msg.GetPageToken()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown token %q", req.Msg.GetPageToken()))
	}
	return connect.NewResponse(page), nil
}

func scopeClient(t *testing.T, s *scopes) *accounts.AccessibleScopesClient {
	return serve(t, func(mux *http.ServeMux) { mux.Handle(accountsv1connect.NewAccessibleScopeServiceHandler(s)) }).AccessibleScopes()
}

func TestListAllDrainsEveryPage(t *testing.T) {
	s := &scopes{pages: map[string]*v1.ListAccessibleScopesResponse{
		"":   {Scopes: []*v1.AccessibleScope{{NodeId: "a", ScopePath: "/a"}}, NextPageToken: "p2"},
		"p2": {Scopes: []*v1.AccessibleScope{{NodeId: "b", ScopePath: "/b"}}},
	}}
	got, err := scopeClient(t, s).ListAll(context.Background(), "org-1", "documents", "read")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].GetNodeId() != "a" || got[1].GetNodeId() != "b" {
		t.Fatalf("scopes = %v", got)
	}
	first := s.requests[0]
	if first.GetOrgId() != "org-1" || first.GetResourceType() != "documents" || first.GetAction() != "read" || first.GetPageSize() != 1000 {
		t.Fatalf("first request = %v", first)
	}
}

func TestListAllEmptyIsAResolvedAnswer(t *testing.T) {
	s := &scopes{pages: map[string]*v1.ListAccessibleScopesResponse{"": {}}}
	got, err := scopeClient(t, s).ListAll(context.Background(), "org-1", "documents", "read")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("an empty grant set must be a non-nil empty slice, got %#v", got)
	}
}

func TestListAllRefusesARepeatedCursor(t *testing.T) {
	s := &scopes{pages: map[string]*v1.ListAccessibleScopesResponse{
		"":   {Scopes: []*v1.AccessibleScope{{NodeId: "a"}}, NextPageToken: "p2"},
		"p2": {Scopes: []*v1.AccessibleScope{{NodeId: "b"}}, NextPageToken: "p2"},
	}}
	got, err := scopeClient(t, s).ListAll(context.Background(), "org-1", "documents", "read")
	if !errors.Is(err, accounts.ErrAccessibleScopePagination) || got != nil {
		t.Fatalf("got %v, %v; want the pagination error and no partial set", got, err)
	}
}

func TestListAllNeverReturnsAPartialSetOnALaterFailure(t *testing.T) {
	s := &scopes{
		pages: map[string]*v1.ListAccessibleScopesResponse{
			"": {Scopes: []*v1.AccessibleScope{{NodeId: "a"}}, NextPageToken: "p2"},
		},
		fail: connect.NewError(connect.CodePermissionDenied, errors.New("denied")),
	}
	got, err := scopeClient(t, s).ListAll(context.Background(), "org-1", "documents", "read")
	if connect.CodeOf(err) != connect.CodePermissionDenied || got != nil {
		t.Fatalf("got %v, %v; want the denial and no partial set", got, err)
	}
}

type workContexts struct {
	accountsv1connect.UnimplementedWorkContextServiceHandler
	req *v1.StartTaskWorkContextRequest
}

func (w *workContexts) StartTask(_ context.Context, req *connect.Request[v1.StartTaskWorkContextRequest]) (*connect.Response[v1.IssuedWorkContext], error) {
	w.req = req.Msg
	return connect.NewResponse(&v1.IssuedWorkContext{Token: "wc", OrgId: req.Msg.GetOrgId(), OwnerPrincipalId: "p-1", CurrentActorPrincipalId: "p-1"}), nil
}

func TestStartTaskUnwraps(t *testing.T) {
	w := &workContexts{}
	c := serve(t, func(mux *http.ServeMux) { mux.Handle(accountsv1connect.NewWorkContextServiceHandler(w)) })
	issued, err := c.WorkContexts().StartTask(context.Background(), &accounts.StartTaskWorkContextRequest{
		OrgId:           "org-1",
		Audience:        "documents",
		AuthorityScopes: []*accounts.WorkContextScope{{ResourceKind: "documents", Actions: []string{"read"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.GetToken() != "wc" || issued.GetOwnerPrincipalId() != "p-1" || w.req.GetAudience() != "documents" {
		t.Fatalf("issued = %v, request = %v", issued, w.req)
	}
}
