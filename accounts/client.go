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
//	resp, err := accounts.New(gw).Audit().QueryAuditLog(ctx, &accounts.QueryAuditLogRequest{PageSize: 20})
//
// Packaging note: the generated stubs under `gen/` are this module's private
// implementation detail. A consumer calls every method here while importing
// only this package — the message types, and everything they expose, are
// re-exported in types.go — so the stub tree can be regenerated against a newer
// contract without touching a line of consumer code.
//
// Replacing the committed tree with a registry-served package is a bigger step
// than a regeneration: the aliases keep consumer *source* unchanged, but two
// packages generated from the same .proto in one binary collide in protobuf's
// global file registry, so that swap has to retire the committed tree rather
// than sit beside it. internal/apiboundary is the gate that keeps the source
// half of this true.
package accounts

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

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
func (a *AuditClient) QueryAuditLog(ctx context.Context, req *QueryAuditLogRequest) (*QueryAuditLogResponse, error) {
	resp, err := a.inner.QueryAuditLog(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Additional services (Identity, Organizations, Teams, ...) follow the same
// two-method pattern: a Client.X() constructor + an XClient facade that unwraps
// the envelope. Codegen can emit these from the same proto the stubs come from.
