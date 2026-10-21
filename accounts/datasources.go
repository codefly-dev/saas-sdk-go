package accounts

import (
	"context"

	"connectrpc.com/connect"

	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
	"github.com/codefly-dev/saas-sdk-go/internal/connectresponse"
)

// Datasources returns the typed DatasourceService facade: the host's source
// connection, operation and account-link contract, message for message.
//
// It is the raw contract. Package datasource is the ergonomic layer over the
// same service, and its Invoke and Lookup are the effect-safe way to call a
// source operation: they mint the effect ID, keep it on every outcome and
// classify an unknown outcome. InvokeSourceOperation here sends exactly the
// request it is given and returns the host's error as Connect decoded it.
//
// Both facades read responses through the same client, so a Connect error
// arrives here exactly as it does there.
func (c *Client) Datasources() *DatasourcesClient {
	return &DatasourcesClient{
		inner: accountsv1connect.NewDatasourceServiceClient(
			connectresponse.Client{HTTP: c.gw.HTTPClient()}, c.gw.BaseURL(), c.opts...),
	}
}

// DatasourcesClient wraps the generated DatasourceServiceClient and hides the
// connect.Request/Response envelope.
type DatasourcesClient struct {
	inner accountsv1connect.DatasourceServiceClient
}

// unary sends one request and returns the bare response message.
func unary[Req, Res any](
	ctx context.Context, req *Req,
	call func(context.Context, *connect.Request[Req]) (*connect.Response[Res], error),
) (*Res, error) {
	resp, err := call(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// ListSources returns the calling organization's connected datasources.
func (d *DatasourcesClient) ListSources(ctx context.Context, req *ListSourcesRequest) (*ListSourcesResponse, error) {
	return unary(ctx, req, d.inner.ListSources)
}

// GetSource returns one connected datasource in the calling organization.
func (d *DatasourcesClient) GetSource(ctx context.Context, req *GetSourceRequest) (*GetSourceResponse, error) {
	return unary(ctx, req, d.inner.GetSource)
}

// AddSource registers a datasource for any provider, configured by the
// request's Config oneof. The host stores the credential encrypted and returns
// the non-secret projection; no read returns the credential.
func (d *DatasourcesClient) AddSource(ctx context.Context, req *AddSourceRequest) (*AddSourceResponse, error) {
	return unary(ctx, req, d.inner.AddSource)
}

// ListSourceOperations reads a source's currently admitted operation
// declarations.
func (d *DatasourcesClient) ListSourceOperations(ctx context.Context, req *ListSourceOperationsRequest) (*ListSourceOperationsResponse, error) {
	return unary(ctx, req, d.inner.ListSourceOperations)
}

// DeclareSourceOperations atomically replaces a source's operation set, as an
// administrator.
func (d *DatasourcesClient) DeclareSourceOperations(ctx context.Context, req *DeclareSourceOperationsRequest) (*DeclareSourceOperationsResponse, error) {
	return unary(ctx, req, d.inner.DeclareSourceOperations)
}

// InvokeSourceOperation calls an admitted operation under the request's effect
// ID. A failure after dispatch does not say whether the provider acted: recover
// the same effect with LookupInvokeSourceOperation, never under a fresh ID. The
// host's refusal reasons are in the ErrorInfo of the returned Connect error.
func (d *DatasourcesClient) InvokeSourceOperation(ctx context.Context, req *InvokeSourceOperationRequest) (*InvokeSourceOperationResponse, error) {
	return unary(ctx, req, d.inner.InvokeSourceOperation)
}

// LookupInvokeSourceOperation recovers an effect's receipt without contacting
// the provider. An unknown receipt never authorizes a redispatch.
func (d *DatasourcesClient) LookupInvokeSourceOperation(ctx context.Context, req *LookupInvokeSourceOperationRequest) (*InvokeSourceOperationResponse, error) {
	return unary(ctx, req, d.inner.LookupInvokeSourceOperation)
}

// BeginDatasourceAccountLink mints a one-time state bound to the organization,
// the calling person and the connector, and returns where to sign in to the
// provider.
func (d *DatasourcesClient) BeginDatasourceAccountLink(ctx context.Context, req *BeginDatasourceAccountLinkRequest) (*BeginDatasourceAccountLinkResponse, error) {
	return unary(ctx, req, d.inner.BeginDatasourceAccountLink)
}

// CompleteDatasourceAccountLink redeems that state and the provider's
// authorization code and links the provider account to the calling person.
func (d *DatasourcesClient) CompleteDatasourceAccountLink(ctx context.Context, req *CompleteDatasourceAccountLinkRequest) (*CompleteDatasourceAccountLinkResponse, error) {
	return unary(ctx, req, d.inner.CompleteDatasourceAccountLink)
}

// ListMyDatasourceAccountLinks returns the calling person's own linked
// provider accounts.
func (d *DatasourcesClient) ListMyDatasourceAccountLinks(ctx context.Context, req *ListMyDatasourceAccountLinksRequest) (*ListMyDatasourceAccountLinksResponse, error) {
	return unary(ctx, req, d.inner.ListMyDatasourceAccountLinks)
}
