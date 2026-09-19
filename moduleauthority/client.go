// Package moduleauthority exchanges a composed module's installed authority
// through the SaaS module-capabilities surface.
package moduleauthority

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	codefly "github.com/codefly-dev/sdk-go"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

const defaultRefreshSkew = 30 * time.Second

var (
	ErrInvalidCredentials = errors.New("module authority: invalid module credentials")
	ErrInvalidExchange    = errors.New("module authority: invalid operation exchange")
	ErrInvalidCapability  = errors.New("module authority: SaaS returned an invalid Work Context")
)

// Gateway is the minimal solution-runtime gateway surface used by the client.
type Gateway interface {
	BaseURL() string
	HTTPClient() *http.Client
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
	inner       accountsv1connect.ModuleCapabilitiesServiceClient
	credentials Credentials
	now         func() time.Time
	refreshSkew time.Duration

	mu        sync.Mutex
	module    codefly.WorkContextToken
	expiresAt time.Time
}

// New binds module authority exchange to the gateway. The gateway supplies
// both service discovery and transport; no service address is configured here.
func New(gw Gateway, credentials Credentials, opts ...connect.ClientOption) *Client {
	return &Client{
		inner:       accountsv1connect.NewModuleCapabilitiesServiceClient(gw.HTTPClient(), gw.BaseURL(), opts...),
		credentials: credentials,
		now:         time.Now,
		refreshSkew: defaultRefreshSkew,
	}
}

// ModuleWorkContext returns a current module capability, refreshing it before
// expiry. Concurrent callers share one refresh.
func (c *Client) ModuleWorkContext(ctx context.Context) (codefly.WorkContextToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.moduleWorkContextLocked(ctx)
}

// ExchangeOperation exchanges the signed-in person's retained parent
// capability through one immutable installed operation binding. A rejected
// module capability is refreshed and retried once; the parent is never stored.
func (c *Client) ExchangeOperation(ctx context.Context, exchange ExchangeRequest) (codefly.WorkContextToken, error) {
	if exchange.BindingID == "" || exchange.Parent.Encoded() == "" {
		return codefly.WorkContextToken{}, ErrInvalidExchange
	}

	module, err := c.ModuleWorkContext(ctx)
	if err != nil {
		return codefly.WorkContextToken{}, err
	}
	issued, err := c.exchange(ctx, module, exchange)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		return issued, err
	}

	c.mu.Lock()
	if c.module.Encoded() == module.Encoded() {
		c.module = codefly.WorkContextToken{}
		c.expiresAt = time.Time{}
	}
	module, refreshErr := c.moduleWorkContextLocked(ctx)
	c.mu.Unlock()
	if refreshErr != nil {
		return codefly.WorkContextToken{}, refreshErr
	}
	return c.exchange(ctx, module, exchange)
}

func (c *Client) moduleWorkContextLocked(ctx context.Context) (codefly.WorkContextToken, error) {
	if c.module.Encoded() != "" && c.now().Add(c.refreshSkew).Before(c.expiresAt) {
		return c.module, nil
	}
	if c.credentials.Prefix == "" || c.credentials.Secret == "" {
		return codefly.WorkContextToken{}, ErrInvalidCredentials
	}

	resp, err := c.inner.MintModuleWorkContext(ctx, connect.NewRequest(&v1.ModuleMintWorkContextRequest{
		Prefix: c.credentials.Prefix,
		Secret: c.credentials.Secret,
	}))
	if err != nil {
		return codefly.WorkContextToken{}, fmt.Errorf("module authority: mint module Work Context: %w", err)
	}
	if resp.Msg.GetExpiresAt() == nil {
		return codefly.WorkContextToken{}, fmt.Errorf("%w: missing expiry", ErrInvalidCapability)
	}
	expiresAt := resp.Msg.GetExpiresAt().AsTime()
	if !expiresAt.After(c.now()) {
		return codefly.WorkContextToken{}, fmt.Errorf("%w: capability is already expired", ErrInvalidCapability)
	}
	token, err := codefly.ParseWorkContextToken(resp.Msg.GetToken())
	if err != nil {
		return codefly.WorkContextToken{}, fmt.Errorf("%w: malformed token", ErrInvalidCapability)
	}
	c.module = token
	c.expiresAt = expiresAt
	return token, nil
}

func (c *Client) exchange(ctx context.Context, module codefly.WorkContextToken, exchange ExchangeRequest) (codefly.WorkContextToken, error) {
	req := connect.NewRequest(&v1.ModuleExchangeDelegatedOperationAudienceRequest{
		BindingId:              exchange.BindingID,
		ParentWorkContextToken: exchange.Parent.Encoded(),
		Lookup:                 exchange.Lookup,
	})
	req.Header().Set(codefly.WorkContextHeaderName, module.Encoded())
	resp, err := c.inner.ExchangeDelegatedOperationAudience(ctx, req)
	if err != nil {
		return codefly.WorkContextToken{}, fmt.Errorf("module authority: exchange operation audience: %w", err)
	}
	token, err := codefly.ParseWorkContextToken(resp.Msg.GetToken())
	if err != nil {
		return codefly.WorkContextToken{}, fmt.Errorf("%w: malformed exchanged token", ErrInvalidCapability)
	}
	return token, nil
}
