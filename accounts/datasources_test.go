package accounts_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"

	"github.com/codefly-dev/saas-sdk-go/accounts"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// datasourceHost answers every DatasourcesClient method with the response the
// test set, or with refusal when it is set, and records what reached it.
type datasourceHost struct {
	accountsv1connect.UnimplementedDatasourceServiceHandler
	response  proto.Message
	refusal   *connect.Error
	procedure string
	request   proto.Message
}

func answer[Req, Res any](h *datasourceHost, req *connect.Request[Req]) (*connect.Response[Res], error) {
	h.procedure = req.Spec().Procedure
	h.request = any(req.Msg).(proto.Message)
	if h.refusal != nil {
		return nil, h.refusal
	}
	return connect.NewResponse(any(h.response).(*Res)), nil
}

func (h *datasourceHost) ListSources(_ context.Context, req *connect.Request[accounts.ListSourcesRequest]) (*connect.Response[accounts.ListSourcesResponse], error) {
	return answer[accounts.ListSourcesRequest, accounts.ListSourcesResponse](h, req)
}

func (h *datasourceHost) GetSource(_ context.Context, req *connect.Request[accounts.GetSourceRequest]) (*connect.Response[accounts.GetSourceResponse], error) {
	return answer[accounts.GetSourceRequest, accounts.GetSourceResponse](h, req)
}

func (h *datasourceHost) AddSource(_ context.Context, req *connect.Request[accounts.AddSourceRequest]) (*connect.Response[accounts.AddSourceResponse], error) {
	return answer[accounts.AddSourceRequest, accounts.AddSourceResponse](h, req)
}

func (h *datasourceHost) ListSourceOperations(_ context.Context, req *connect.Request[accounts.ListSourceOperationsRequest]) (*connect.Response[accounts.ListSourceOperationsResponse], error) {
	return answer[accounts.ListSourceOperationsRequest, accounts.ListSourceOperationsResponse](h, req)
}

func (h *datasourceHost) DeclareSourceOperations(_ context.Context, req *connect.Request[accounts.DeclareSourceOperationsRequest]) (*connect.Response[accounts.DeclareSourceOperationsResponse], error) {
	return answer[accounts.DeclareSourceOperationsRequest, accounts.DeclareSourceOperationsResponse](h, req)
}

func (h *datasourceHost) InvokeSourceOperation(_ context.Context, req *connect.Request[accounts.InvokeSourceOperationRequest]) (*connect.Response[accounts.InvokeSourceOperationResponse], error) {
	return answer[accounts.InvokeSourceOperationRequest, accounts.InvokeSourceOperationResponse](h, req)
}

func (h *datasourceHost) LookupInvokeSourceOperation(_ context.Context, req *connect.Request[accounts.LookupInvokeSourceOperationRequest]) (*connect.Response[accounts.InvokeSourceOperationResponse], error) {
	return answer[accounts.LookupInvokeSourceOperationRequest, accounts.InvokeSourceOperationResponse](h, req)
}

func (h *datasourceHost) BeginDatasourceAccountLink(_ context.Context, req *connect.Request[accounts.BeginDatasourceAccountLinkRequest]) (*connect.Response[accounts.BeginDatasourceAccountLinkResponse], error) {
	return answer[accounts.BeginDatasourceAccountLinkRequest, accounts.BeginDatasourceAccountLinkResponse](h, req)
}

func (h *datasourceHost) CompleteDatasourceAccountLink(_ context.Context, req *connect.Request[accounts.CompleteDatasourceAccountLinkRequest]) (*connect.Response[accounts.CompleteDatasourceAccountLinkResponse], error) {
	return answer[accounts.CompleteDatasourceAccountLinkRequest, accounts.CompleteDatasourceAccountLinkResponse](h, req)
}

func (h *datasourceHost) ListMyDatasourceAccountLinks(_ context.Context, req *connect.Request[accounts.ListMyDatasourceAccountLinksRequest]) (*connect.Response[accounts.ListMyDatasourceAccountLinksResponse], error) {
	return answer[accounts.ListMyDatasourceAccountLinksRequest, accounts.ListMyDatasourceAccountLinksResponse](h, req)
}

type gateway struct {
	base   string
	client *http.Client
}

func (g gateway) BaseURL() string          { return g.base }
func (g gateway) HTTPClient() *http.Client { return g.client }

func newDatasources(t *testing.T) (*accounts.DatasourcesClient, *datasourceHost) {
	t.Helper()
	host := &datasourceHost{}
	mux := http.NewServeMux()
	mux.Handle(accountsv1connect.NewDatasourceServiceHandler(host))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return accounts.New(gateway{base: server.URL, client: server.Client()}).Datasources(), host
}

// datasourceCall is one DatasourcesClient method: the procedure it must reach,
// the request a consumer builds from package accounts alone, and the response
// the host answers.
type datasourceCall struct {
	procedure string
	request   proto.Message
	response  proto.Message
	call      func(context.Context, *accounts.DatasourcesClient, proto.Message) (proto.Message, error)
}

func call[Req, Res any](method func(*accounts.DatasourcesClient, context.Context, *Req) (*Res, error)) func(context.Context, *accounts.DatasourcesClient, proto.Message) (proto.Message, error) {
	return func(ctx context.Context, client *accounts.DatasourcesClient, request proto.Message) (proto.Message, error) {
		response, err := method(client, ctx, any(request).(*Req))
		if response == nil {
			return nil, err
		}
		return any(response).(proto.Message), err
	}
}

func datasourceCalls() map[string]datasourceCall {
	source := &accounts.Datasource{Id: "ds-1", OrgId: "org-1", Provider: accounts.DatasourceProvider_DATASOURCE_PROVIDER_API}
	operation := &accounts.SourceOperation{Name: "list_invoices", Effect: accounts.SourceOperation_EFFECT_READ_ONLY}
	receipt := &accounts.InvokeSourceOperationResponse{
		OutputJson: `{"invoices":[]}`,
		Receipt:    &accounts.SourceOperationReceipt{EffectId: "effect-1"},
	}
	link := &accounts.DatasourceAccountLink{Id: "link-1", OrgId: "org-1", Connector: "api"}
	return map[string]datasourceCall{
		"ListSources": {
			procedure: accountsv1connect.DatasourceServiceListSourcesProcedure,
			request:   &accounts.ListSourcesRequest{OrgId: "org-1"},
			response:  &accounts.ListSourcesResponse{Datasources: []*accounts.Datasource{source}},
			call:      call((*accounts.DatasourcesClient).ListSources),
		},
		"GetSource": {
			procedure: accountsv1connect.DatasourceServiceGetSourceProcedure,
			request:   &accounts.GetSourceRequest{OrgId: "org-1", Id: "ds-1"},
			response:  &accounts.GetSourceResponse{Datasource: source},
			call:      call((*accounts.DatasourcesClient).GetSource),
		},
		"AddSource": {
			procedure: accountsv1connect.DatasourceServiceAddSourceProcedure,
			request: &accounts.AddSourceRequest{
				OrgId:    "org-1",
				Provider: accounts.DatasourceProvider_DATASOURCE_PROVIDER_API,
				Config: &accounts.AddSourceRequest_Api{Api: &accounts.ApiDatasourceConfig{
					BaseUrl:        "https://api.example.com",
					CredentialKind: accounts.ApiCredentialKind_API_CREDENTIAL_KIND_OAUTH2,
					Oauth2: &accounts.ApiOAuth2Config{
						Grant:    accounts.ApiOAuth2Config_GRANT_AUTHORIZATION_CODE,
						TokenUrl: "https://api.example.com/oauth/token",
						ClientId: "client-1",
					},
				}},
				Boundary:   &accounts.AddSourceRequest_CollectionLabel{CollectionLabel: "invoices"},
				Credential: "client-secret",
			},
			response: &accounts.AddSourceResponse{Datasource: source},
			call:     call((*accounts.DatasourcesClient).AddSource),
		},
		"ListSourceOperations": {
			procedure: accountsv1connect.DatasourceServiceListSourceOperationsProcedure,
			request:   &accounts.ListSourceOperationsRequest{OrgId: "org-1", SourceId: "ds-1"},
			response:  &accounts.ListSourceOperationsResponse{Operations: []*accounts.SourceOperation{operation}},
			call:      call((*accounts.DatasourcesClient).ListSourceOperations),
		},
		"DeclareSourceOperations": {
			procedure: accountsv1connect.DatasourceServiceDeclareSourceOperationsProcedure,
			request:   &accounts.DeclareSourceOperationsRequest{OrgId: "org-1", SourceId: "ds-1", Operations: []*accounts.SourceOperation{operation}},
			response:  &accounts.DeclareSourceOperationsResponse{Operations: []*accounts.SourceOperation{operation}},
			call:      call((*accounts.DatasourcesClient).DeclareSourceOperations),
		},
		"InvokeSourceOperation": {
			procedure: accountsv1connect.DatasourceServiceInvokeSourceOperationProcedure,
			request: &accounts.InvokeSourceOperationRequest{
				OrgId: "org-1", SourceId: "ds-1", Operation: "list_invoices",
				InputJson: `{"limit":20}`, EffectId: "effect-1",
			},
			response: receipt,
			call:     call((*accounts.DatasourcesClient).InvokeSourceOperation),
		},
		"LookupInvokeSourceOperation": {
			procedure: accountsv1connect.DatasourceServiceLookupInvokeSourceOperationProcedure,
			request:   &accounts.LookupInvokeSourceOperationRequest{EffectId: "effect-1"},
			response:  receipt,
			call:      call((*accounts.DatasourcesClient).LookupInvokeSourceOperation),
		},
		"BeginDatasourceAccountLink": {
			procedure: accountsv1connect.DatasourceServiceBeginDatasourceAccountLinkProcedure,
			request:   &accounts.BeginDatasourceAccountLinkRequest{OrgId: "org-1", Connector: "api", RedirectUri: "https://app.example.com/linked", SourceId: "ds-1"},
			response:  &accounts.BeginDatasourceAccountLinkResponse{AuthorizeUrl: "https://api.example.com/oauth/authorize", State: "state-1"},
			call:      call((*accounts.DatasourcesClient).BeginDatasourceAccountLink),
		},
		"CompleteDatasourceAccountLink": {
			procedure: accountsv1connect.DatasourceServiceCompleteDatasourceAccountLinkProcedure,
			request:   &accounts.CompleteDatasourceAccountLinkRequest{OrgId: "org-1", State: "state-1", Code: "code-1"},
			response:  &accounts.CompleteDatasourceAccountLinkResponse{Link: link},
			call:      call((*accounts.DatasourcesClient).CompleteDatasourceAccountLink),
		},
		"ListMyDatasourceAccountLinks": {
			procedure: accountsv1connect.DatasourceServiceListMyDatasourceAccountLinksProcedure,
			request:   &accounts.ListMyDatasourceAccountLinksRequest{OrgId: "org-1"},
			response:  &accounts.ListMyDatasourceAccountLinksResponse{Links: []*accounts.DatasourceAccountLink{link}},
			call:      call((*accounts.DatasourcesClient).ListMyDatasourceAccountLinks),
		},
	}
}

// TestDatasourcesReachTheirProcedureAndUnwrap: each method sends the request it
// is given, unchanged, to its own procedure, and returns the host's response
// message as it was sent.
func TestDatasourcesReachTheirProcedureAndUnwrap(t *testing.T) {
	for name, tc := range datasourceCalls() {
		t.Run(name, func(t *testing.T) {
			client, host := newDatasources(t)
			host.response = tc.response
			got, err := tc.call(t.Context(), client, tc.request)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if host.procedure != tc.procedure {
				t.Errorf("procedure = %q, want %q", host.procedure, tc.procedure)
			}
			if !proto.Equal(host.request, tc.request) {
				t.Errorf("request reaching the host = %v, want %v", host.request, tc.request)
			}
			if !proto.Equal(got, tc.response) {
				t.Errorf("response = %v, want %v", got, tc.response)
			}
		})
	}
}

// TestDatasourcesPreserveTheHostsConnectError: a refusal arrives as the host's
// own Connect error — its code, its message and its ErrorInfo — with no
// response, so a caller keys on the reason exactly as the contract documents.
func TestDatasourcesPreserveTheHostsConnectError(t *testing.T) {
	for name, tc := range datasourceCalls() {
		t.Run(name, func(t *testing.T) {
			client, host := newDatasources(t)
			refusal := connect.NewError(connect.CodeFailedPrecondition, errors.New("source operation outcome unknown"))
			detail, err := connect.NewErrorDetail(&errdetails.ErrorInfo{
				Reason: "SOURCE_OPERATION_OUTCOME_UNKNOWN", Domain: "saas.accounts.v1",
			})
			if err != nil {
				t.Fatal(err)
			}
			refusal.AddDetail(detail)
			host.refusal = refusal

			got, err := tc.call(t.Context(), client, tc.request)
			if got != nil {
				t.Errorf("a refused call returned a response: %v", got)
			}
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != connect.CodeFailedPrecondition || connectErr.Message() != "source operation outcome unknown" {
				t.Errorf("error = %v, want the host's code and message", connectErr)
			}
			var reason string
			for _, detail := range connectErr.Details() {
				if info, ok := must(detail.Value()).(*errdetails.ErrorInfo); ok {
					reason = info.GetReason()
				}
			}
			if reason != "SOURCE_OPERATION_OUTCOME_UNKNOWN" {
				t.Errorf("ErrorInfo reason = %q, want the host's", reason)
			}
			if host.procedure != tc.procedure {
				t.Errorf("procedure = %q, want %q", host.procedure, tc.procedure)
			}
		})
	}
}

func must(value proto.Message, err error) proto.Message {
	if err != nil {
		panic(err)
	}
	return value
}
