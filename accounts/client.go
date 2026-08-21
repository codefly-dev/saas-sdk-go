// Package accounts is the typed, gateway-bound client facade for the saas
// accounts API — the "syntactic sugar" layer over the generated Connect stubs.
//
// It answers two problems in the current solution code:
//  1. handlers pass raw procedure strings to solution.Unary — no discovery, easy
//     to typo (see lastlogin's `const queryAuditLog = "/saas.accounts.v1..."`);
//  2. every solution regenerates and vendors the accounts `gen/` tree.
//
// The facade binds the *generated* Connect clients to a solution runtime
// Gateway (its HTTPClient injects the bearer; its BaseURL points at the gateway)
// and unwraps connect.Request/Response so a handler writes plain protos:
//
//	resp, err := accounts.New(gw).Audit().QueryAuditLog(ctx, &v1.QueryAuditLogRequest{PageSize: 20})
//
// Packaging note (RD5): this package lives in codefly-dev alongside the
// generated accounts SDK. The `gen/` import path below is a placeholder for the
// published accounts SDK module — solutions depend on it, they do not vendor it.
package accounts

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// Gateway is the minimal surface this SDK needs from the solution runtime.
// *github.com/codefly-dev/solution-runtime-go.Gateway satisfies it as-is
// (BaseURL() string, HTTPClient() *http.Client) — so the generic runtime stays
// accounts-agnostic and takes no dependency on this package.
type Gateway interface {
	BaseURL() string
	HTTPClient() *http.Client
}

// Client is the entry point: accounts.New(gw).Audit()....
type Client struct {
	gw   Gateway
	opts []connect.ClientOption
}

// New binds the accounts SDK to a gateway. Extra connect.ClientOptions (e.g.
// connect.WithGRPC()) are forwarded to every sub-client.
func New(gw Gateway, opts ...connect.ClientOption) *Client {
	return &Client{gw: gw, opts: opts}
}

// Audit returns the typed AuditService facade.
func (c *Client) Audit() *AuditClient {
	return &AuditClient{
		inner: accountsv1connect.NewAuditServiceClient(c.gw.HTTPClient(), c.gw.BaseURL(), c.opts...),
	}
}

// AuditClient wraps the generated AuditServiceClient and hides the
// connect.Request/Response envelope.
type AuditClient struct {
	inner accountsv1connect.AuditServiceClient
}

// QueryAuditLog calls saas.accounts.v1.AuditService.QueryAuditLog through the
// gateway and returns the bare response message.
func (a *AuditClient) QueryAuditLog(ctx context.Context, req *v1.QueryAuditLogRequest) (*v1.QueryAuditLogResponse, error) {
	resp, err := a.inner.QueryAuditLog(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Additional services (Identity, Organizations, Teams, ...) follow the same
// two-method pattern: a Client.X() constructor + an XClient facade that unwraps
// the envelope. Codegen can emit these from the same proto the stubs come from.
