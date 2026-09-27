// Package moduleauthority exchanges a composed module's installed authority
// with the SaaS host, over the two seams the host actually serves a composed
// module:
//
//   - the gateway's module broker (REST, JSON over HTTP on the gateway base
//     URL) — where a module presents its identity secret and is minted a Work
//     Context: POST /modules/_work-context, /modules/_operation-context and
//     /modules/_source-operation-context. The Mint* RPCs of
//     ModuleCapabilitiesService are EXPOSURE_INTERNAL and are never served at
//     the gateway edge; the broker is how a module reaches them.
//   - accounts' named `authority` gRPC endpoint — where a module calls, with
//     its module Work Context, the procedures the host exports to composed
//     modules (ExchangeDelegatedOperationAudience among them).
//
// Neither address is configured here: the consumer passes what Codefly
// resolved for it, in Seams.
package moduleauthority

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	codefly "github.com/codefly-dev/sdk-go/workcontext"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

const defaultRefreshSkew = 30 * time.Second

var (
	// ErrInvalidCredentials: the module's credentials are missing, or the
	// broker refused to prove the module (HTTP 401 — a wrong identity secret or
	// a wrong internal token; the host does not say which).
	ErrInvalidCredentials = errors.New("module authority: invalid module credentials")
	// ErrInvalidExchange: the request names no binding, parent or source.
	ErrInvalidExchange = errors.New("module authority: invalid operation exchange")
	// ErrInvalidCapability: SaaS answered with a Work Context this client
	// refuses to hand out (malformed, expired, or issued for something else).
	ErrInvalidCapability = errors.New("module authority: SaaS returned an invalid Work Context")
	// ErrPermissionDenied: the module is proven but may not have what it asked
	// for (HTTP 403 from the operation mint: a binding that declares no
	// headless scopes, or one the module does not hold).
	ErrPermissionDenied = errors.New("module authority: permission denied")
	// ErrInvalidSeams: New was given an incomplete or unsafe seam. See Seams.
	ErrInvalidSeams = errors.New("module authority: invalid seams")
	// ErrNoAuthority: a call needs accounts' authority endpoint, and this client
	// was built without one (a mint-only client).
	ErrNoAuthority = errors.New("module authority: this client has no authority endpoint")
)

// Gateway is the host gateway seam: its REST base URL and the HTTP client to
// reach it with, as the consumer's runtime resolved them. solution-runtime's
// gateway satisfies it.
type Gateway interface {
	BaseURL() string
	HTTPClient() *http.Client
}

// Seams are the host addresses and the perimeter credential this client
// talks to. Every field is resolved by the consumer from its Codefly
// composition; nothing has a default.
type Seams struct {
	// Gateway is the host auth-gateway's REST endpoint, where the module
	// broker mints Work Contexts. Required.
	Gateway Gateway
	// Authority is accounts' `authority` gRPC endpoint, which serves the
	// exchanges and every other Work-Context-authenticated call. A module that
	// only mints (the broker's work) leaves it zero: New then builds a
	// mint-only client, and every call that needs the endpoint fails with
	// ErrNoAuthority instead of dialling an address nobody vouched for.
	Authority Authority
	// InternalToken is the host's internal perimeter credential, sent as
	// X-Codefly-Internal-Token to both seams. Required.
	InternalToken string
	// AllowInsecureHTTP is the consumer's explicit assertion that plaintext
	// hops to a non-loopback address are protected out of band (every
	// in-cluster hop carried by a mutually authenticated mesh). It must come
	// from the consumer's own configured assertion — never inferred. Without
	// it, the internal token and the module secret are sent only over https
	// (or TLS) or to a loopback address, and New refuses anything else.
	AllowInsecureHTTP bool
}

// Authority addresses accounts' `authority` gRPC endpoint.
type Authority struct {
	// Address is the endpoint's host:port, with no scheme.
	Address string
	// TLS, when set, dials the endpoint with this TLS configuration. When nil
	// the endpoint is dialled as HTTP/2 cleartext (h2c) — the host's in-cluster
	// contract — which Seams admits only for a loopback address or under
	// AllowInsecureHTTP.
	TLS *tls.Config
}

// Credentials are the Codefly-projected identity of the calling module.
type Credentials struct {
	Prefix string
	Secret string
}

// ExchangeRequest selects one installed operation binding. SaaS derives the
// audience, scopes, and lifetime from the installation rather than accepting
// them from the caller.
type ExchangeRequest struct {
	BindingID string
	Parent    codefly.WorkContextToken
	Lookup    bool
}

// Client keeps only the module's short-lived Work Context in memory. Parent
// and exchanged capabilities are request-local and are never cached.
type Client struct {
	broker      broker
	authority   authorityEndpoint
	credentials Credentials
	now         func() time.Time
	refreshSkew time.Duration

	mu        sync.Mutex
	module    codefly.WorkContextToken
	expiresAt time.Time
}

// New binds module authority to the host's two seams. It fails closed, with
// ErrInvalidSeams, on a missing gateway, gateway base URL or internal token,
// on an authority seam that is set but has no address, and on any plaintext seam to a non-loopback address the
// consumer has not asserted protected; and with ErrInvalidCredentials on an
// empty prefix or secret. It opens no connection.
func New(seams Seams, credentials Credentials) (*Client, error) {
	if credentials.Prefix == "" || credentials.Secret == "" {
		return nil, ErrInvalidCredentials
	}
	if seams.InternalToken == "" {
		return nil, fmt.Errorf("%w: the internal token is required", ErrInvalidSeams)
	}
	gateway, err := newBroker(seams)
	if err != nil {
		return nil, err
	}
	var authority authorityEndpoint
	if seams.Authority != (Authority{}) {
		if authority, err = newAuthorityEndpoint(seams); err != nil {
			return nil, err
		}
	}
	return &Client{
		broker:      gateway,
		authority:   authority,
		credentials: credentials,
		now:         time.Now,
		refreshSkew: defaultRefreshSkew,
	}, nil
}

// Close releases the idle connections this client holds to the authority
// endpoint. The gateway's HTTP client is the consumer's and is left alone.
func (c *Client) Close() error {
	c.authority.close()
	return nil
}

// ModuleWorkContext returns a current module capability, refreshing it before
// expiry. Concurrent callers share one refresh.
func (c *Client) ModuleWorkContext(ctx context.Context) (codefly.WorkContextToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.moduleWorkContextLocked(ctx)
}

// ExchangeOperation exchanges the signed-in person's retained parent
// capability through one immutable installed operation binding, on the
// authority endpoint. A rejected module capability is refreshed and retried
// once; the parent is never stored.
func (c *Client) ExchangeOperation(ctx context.Context, exchange ExchangeRequest) (codefly.WorkContextToken, error) {
	if exchange.BindingID == "" || exchange.Parent.Encoded() == "" {
		return codefly.WorkContextToken{}, ErrInvalidExchange
	}
	issued, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.ExchangeDelegatedOperationAudience, &v1.ModuleExchangeDelegatedOperationAudienceRequest{
		BindingId:              exchange.BindingID,
		ParentWorkContextToken: exchange.Parent.Encoded(),
		Lookup:                 exchange.Lookup,
	})
	if err != nil {
		return codefly.WorkContextToken{}, fmt.Errorf("module authority: exchange operation audience: %w", err)
	}
	token, err := codefly.ParseWorkContextToken(issued.GetToken())
	if err != nil {
		return codefly.WorkContextToken{}, fmt.Errorf("%w: malformed exchanged token", ErrInvalidCapability)
	}
	return token, nil
}

// OperationContext is a Work Context minted with no person present for one
// installed operation binding, with what SaaS reports it asserts.
type OperationContext struct {
	Token       codefly.WorkContextToken
	ExpiresAt   time.Time
	PrincipalID string
	Tenant      string
	Audience    string
	BindingID   string
}

// MintModuleOperationContext obtains, with no person present, a Work Context
// addressed to one of the module's installed operation audiences — the
// capability background work presents to another module's service. It
// authenticates with the module's own credentials at the gateway broker
// (POST /modules/_operation-context), exactly like ModuleWorkContext; SaaS
// derives audience, scopes (the binding's headless_scopes and nothing else),
// tenant and a short lifetime from the installation. A binding that declares
// no headless scopes, or that the module does not hold, is refused with
// ErrPermissionDenied.
//
// The result is never cached: it lives about a minute, so a caller mints one
// per call or short batch and discards it.
func (c *Client) MintModuleOperationContext(ctx context.Context, bindingID string) (OperationContext, error) {
	if bindingID == "" {
		return OperationContext{}, ErrInvalidExchange
	}
	var issued operationContextResponse
	if err := c.broker.post(ctx, operationContextPath, c.credentials.Secret, map[string]string{
		"prefix":  c.credentials.Prefix,
		"binding": bindingID,
	}, &issued); err != nil {
		return OperationContext{}, fmt.Errorf("module authority: mint operation context: %w", brokerRefusal(err))
	}
	op, err := issued.operationContext(c.now())
	if err != nil {
		return OperationContext{}, err
	}
	if op.BindingID != bindingID {
		return OperationContext{}, fmt.Errorf("%w: issued for another binding", ErrInvalidCapability)
	}
	return op, nil
}

func (c *Client) moduleWorkContextLocked(ctx context.Context) (codefly.WorkContextToken, error) {
	if c.module.Encoded() != "" && c.now().Add(c.refreshSkew).Before(c.expiresAt) {
		return c.module, nil
	}
	if c.credentials.Prefix == "" || c.credentials.Secret == "" {
		return codefly.WorkContextToken{}, ErrInvalidCredentials
	}
	var issued workContextResponse
	if err := c.broker.post(ctx, workContextPath, c.credentials.Secret, map[string]string{
		"prefix": c.credentials.Prefix,
	}, &issued); err != nil {
		return codefly.WorkContextToken{}, fmt.Errorf("module authority: mint module Work Context: %w", brokerRefusal(err))
	}
	expiresAt, err := parseExpiry(issued.ExpiresAt, c.now())
	if err != nil {
		return codefly.WorkContextToken{}, err
	}
	token, err := codefly.ParseWorkContextToken(issued.Token)
	if err != nil {
		return codefly.WorkContextToken{}, fmt.Errorf("%w: malformed token", ErrInvalidCapability)
	}
	c.module = token
	c.expiresAt = expiresAt
	return token, nil
}

// forgetModuleWorkContext drops the cached module capability if it is still
// the one a call was rejected with, so the next caller mints a fresh one. A
// capability another caller already refreshed is kept.
func (c *Client) forgetModuleWorkContext(rejected codefly.WorkContextToken) {
	if c.module.Encoded() == rejected.Encoded() {
		c.module = codefly.WorkContextToken{}
		c.expiresAt = time.Time{}
	}
}
