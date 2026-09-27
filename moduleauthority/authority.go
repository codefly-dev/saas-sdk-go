package moduleauthority

import (
	"context"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	codefly "github.com/codefly-dev/sdk-go/workcontext"

	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// authorityEndpoint is this client's connection to accounts' `authority`
// gRPC endpoint: the generated ModuleCapabilitiesService client, speaking the
// gRPC protocol over HTTP/2 (TLS, or h2c where the transport rule admits it).
//
// Every procedure the host serves there authenticates the calling module from
// its module Work Context; call them through callAsModule, never directly, so
// the credentials and the refresh-once rule are applied the same way to each.
type authorityEndpoint struct {
	capabilities accountsv1connect.ModuleCapabilitiesServiceClient
	transport    *http.Transport
	token        string
}

func newAuthorityEndpoint(seams Seams) (authorityEndpoint, error) {
	base, err := authorityBaseURL(seams.Authority, seams.AllowInsecureHTTP)
	if err != nil {
		return authorityEndpoint{}, err
	}
	protocols := new(http.Protocols)
	transport := &http.Transport{
		// No proxy: the credentials go to the address that was judged, only.
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}
	if seams.Authority.TLS != nil {
		transport.TLSClientConfig = seams.Authority.TLS.Clone()
		protocols.SetHTTP2(true)
	} else {
		protocols.SetUnencryptedHTTP2(true)
	}
	transport.Protocols = protocols
	httpClient := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return authorityEndpoint{
		capabilities: accountsv1connect.NewModuleCapabilitiesServiceClient(httpClient, base, connect.WithGRPC()),
		transport:    transport,
		token:        seams.InternalToken,
	}, nil
}

func (a authorityEndpoint) close() {
	if a.transport != nil {
		a.transport.CloseIdleConnections()
	}
}

// callAsModule calls one procedure on the authority endpoint as this module:
// it attaches the internal token (x-codefly-internal-token) and the current
// module Work Context (x-codefly-work-context), and when the host rejects that
// Work Context (Unauthenticated) it drops it, mints a fresh one at the broker
// and retries exactly once. Any other error is returned as is — in particular
// PermissionDenied, which is how the endpoint refuses a wrong internal token
// or a procedure it does not serve, and which a refresh cannot change.
//
// It is the one way this package reaches the authority endpoint. A new facade
// method over a procedure the host serves there passes the generated client's
// method and the request message:
//
//	res, err := callAsModule(ctx, c, c.authority.capabilities.SomeProcedure, &v1.SomeRequest{...})
func callAsModule[Req, Res any](
	ctx context.Context,
	c *Client,
	call func(context.Context, *connect.Request[Req]) (*connect.Response[Res], error),
	msg *Req,
) (*Res, error) {
	module, err := c.ModuleWorkContext(ctx)
	if err != nil {
		return nil, err
	}
	res, err := call(ctx, authorityRequest(c.authority, module, msg))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}

	c.mu.Lock()
	c.forgetModuleWorkContext(module)
	module, err = c.moduleWorkContextLocked(ctx)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	res, err = call(ctx, authorityRequest(c.authority, module, msg))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// authorityRequest wraps msg with the two credentials the endpoint requires.
// A fresh request per attempt, so a retry never carries the rejected token.
func authorityRequest[Req any](a authorityEndpoint, module codefly.WorkContextToken, msg *Req) *connect.Request[Req] {
	req := connect.NewRequest(msg)
	req.Header().Set(internalTokenHeader, a.token)
	req.Header().Set(codefly.WorkContextHeaderName, module.Encoded())
	return req
}
