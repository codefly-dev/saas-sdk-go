// Package moduleauthority exchanges a composed module's installed authority
// with the SaaS host, through a broker and an explicitly selected authority
// transport:
//
//   - the gateway's module broker (REST, JSON over HTTP on the gateway base
//     URL) — where a module presents its pod's projected ServiceAccount token
//     and is minted a Work Context: POST /modules/_work-context,
//     /modules/_operation-context and
//     /modules/_source-operation-context. The Mint* RPCs of
//     ModuleCapabilitiesService are EXPOSURE_INTERNAL and are never served at
//     the gateway edge; the broker is how a module reaches them.
//   - accounts' named `authority` gRPC endpoint — where a module calls, with
//     its module Work Context, the procedures the host exports to composed
//     modules (ExchangeDelegatedOperationAudience among them). New selects this
//     transport. NewGatewayAuthority instead uses the host gateway bridge for
//     procedures that the selected host exposes there, without direct fallback.
//
// Neither address is configured here: the consumer passes what Codefly
// resolved for it, in Seams.
package moduleauthority

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

const defaultRefreshSkew = 30 * time.Second

var (
	// ErrInvalidCredentials: the module's credentials are missing, its
	// OrganizationID is not a canonical UUID, or its projected token could not
	// be read, is empty or exceeds 64 KiB; or the broker refused to prove the
	// module (HTTP 401 — a token the host refused, another workload's token, a
	// pod running a build the host does not approve, a wrong internal token, or,
	// on the module Work Context mint, an organization the module may not mint
	// for: a shared module that names none, or a tenant-pinned module that
	// names another. The host does not say which). The operation mint refuses
	// that organization case with ErrPermissionDenied (HTTP 403) instead.
	ErrInvalidCredentials = errors.New("module authority: invalid module credentials")
	// ErrExecutionUnestablished: the host could not establish what this pod is
	// running (HTTP 503) — its API server unreachable, or no execution binding
	// wired. Nothing was decided about the module; minting again may succeed.
	ErrExecutionUnestablished = errors.New("module authority: the host could not establish this pod's execution")
	// ErrInvalidExchange: the request names no binding or source, or does not
	// present exactly one of a parent capability and a delegation reference.
	ErrInvalidExchange = errors.New("module authority: invalid operation exchange")
	// ErrInvalidCapability: SaaS answered with a Work Context this client
	// refuses to hand out (malformed, expired, or issued for something else).
	ErrInvalidCapability = errors.New("module authority: SaaS returned an invalid Work Context")
	// ErrNoHostStateToMintFor: the module is PROVEN and the host holds nothing
	// to mint it a capability against (HTTP 412, which relays accounts'
	// FailedPrecondition) — most usually a workload the host admits that is
	// installed in no organization it may mint for, or a tenant that names no
	// organization. Nothing is wrong with the credential and nothing is denied:
	// a mint succeeds once the module is installed. It is distinct from
	// ErrPermissionDenied for that reason, and from a 502, which is the gateway
	// saying the host itself failed.
	ErrNoHostStateToMintFor = errors.New("module authority: the host holds no state to mint this module a capability against")
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

// Gateway is the host workload gateway seam: its HTTP base URL and client,
// as the consumer's runtime resolved them. A public gateway URL has the same
// shape but does not serve the module credential exchanges.
type Gateway interface {
	BaseURL() string
	HTTPClient() *http.Client
}

// Seams are the host addresses and the perimeter credential this client
// talks to. Every field is resolved by the consumer from its Codefly
// composition; nothing has a default.
type Seams struct {
	// Gateway is the host auth-gateway's workload HTTP endpoint, where the
	// module broker mints Work Contexts. The consumer resolves this endpoint;
	// the public rest endpoint does not serve these exchanges. Required.
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
	// it, the internal token and the projected token are sent only over https
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

// Credentials are the identity of the calling module: the prefix it acts as,
// and the source of the ServiceAccount token Kubernetes projects into its pod
// for the `accounts` audience. The host reviews that token, reads the pod it
// names and mints only if the pod is the prefix's declared workload running the
// build its authority approves. There is no secret: the module identity secret
// is deleted host-side.
type Credentials struct {
	Prefix string
	Token  ProjectedTokenSource
	// OrganizationID selects the organization for module Work Context and
	// operation-context mints, as a canonical (lowercase, hyphenated) UUID;
	// New refuses any other form with ErrInvalidCredentials, as accounts would,
	// even for a client that only mints source operation contexts: a
	// misconfigured credential fails at construction, not at whichever mint
	// first sends it.
	// Required for a shared module; a tenant-pinned module may omit it or name
	// its own tenant. The host checks the module's envelope and live
	// installation; this selector grants no authority. A missing or wrong
	// organization is refused as unproven credentials: ErrInvalidCredentials
	// (HTTP 401) on the module Work Context mint, ErrPermissionDenied (HTTP 403)
	// on the operation mint.
	// Use a separate Client for each organization: its cached module Work
	// Context belongs to that organization. Source-context mints ignore this
	// field because the source's delegation determines their organization.
	OrganizationID string
}

// ProjectedTokenSource reads the projected ServiceAccount token as it is NOW.
// It is called before every mint rather than once, because the kubelet rotates
// the projection under a running process. Its method set is
// sdk-go/workcontext's ProjectedTokenSource's, so that implementation satisfies
// it as written — this module does not import sdk-go/workcontext.
// Empty tokens and tokens larger than 64 KiB after trimming whitespace are
// refused with ErrInvalidCredentials before any broker request is sent.
type ProjectedTokenSource interface {
	ProjectedToken() (string, error)
}

// ProjectedTokenFile reads the token from the path its projected volume is
// mounted at, trimmed. An empty file is an error, never an empty token: a
// projection mid-rotation must not be presented as a credential.
// The value is the path to read; ProjectedTokenFile itself has no default.
// The file read is limited to 64 KiB, including surrounding whitespace.
//
// It is the same contract as sdk-go/workcontext's ProjectedTokenFile; a
// consumer that already imports that module may pass that one instead. This
// one exists because moduleauthority does not import sdk-go/workcontext: it
// carries Work Contexts as opaque strings and reads them only through core.
//
// A module-identity service running under Codefly passes WorkloadTokenFile(),
// which names the path Codefly projected its token to in a cell's pod and in
// a native run; see WorkloadTokenFile for the one case it does not cover.
type ProjectedTokenFile string

// WorkloadTokenFileVariable is the environment variable that names the
// module's projected token file to a process that cannot read it at the path a
// pod mounts it at. A native `codefly run` stands in for the cell (codefly-dev/cli
// v0.1.187, #977): it projects each module workload's token to a file of its
// own and names that file here. codefly-dev/solution-runtime-go reads the same
// variable (ProjectedTokenFileEnvironmentVariable).
const WorkloadTokenFileVariable = "CODEFLY__WORKLOAD_TOKEN_FILE"

// PodWorkloadTokenFile is the path a cell's rendered pod mounts the module's
// projected ServiceAccount token at, read-only, for audience `accounts`.
const PodWorkloadTokenFile = "/var/run/secrets/codefly/token"

// WorkloadTokenFile is where a module-identity service's projected token is:
// the file WorkloadTokenFileVariable names when the variable is set to a
// non-blank value, and PodWorkloadTokenFile otherwise. A process under a native
// run is told; a pod is not, and reads its mount.
//
// Its scope is exactly the services a native run names the variable for: a
// service that declares module-identity and does not declare
// workload-identity (codefly-dev/cli cmd/run/native_cluster.go). A service
// that declares workload-identity is not given the variable; a native run
// delivers its token's path as TOKEN_FILE in its workload-identity
// configuration instead, so for it this helper falls back to
// PodWorkloadTokenFile, a path that does not exist on the machine running it.
// Such a service passes ProjectedTokenFile with the path that configuration
// names.
//
// It resolves the path, never the token: the file is still read before every
// mint, because the projection rotates under a running process.
func WorkloadTokenFile() ProjectedTokenFile {
	if path := strings.TrimSpace(os.Getenv(WorkloadTokenFileVariable)); path != "" {
		return ProjectedTokenFile(path)
	}
	return ProjectedTokenFile(PodWorkloadTokenFile)
}

// ProjectedToken reads and trims the file, bounded.
func (path ProjectedTokenFile) ProjectedToken() (string, error) {
	if strings.TrimSpace(string(path)) == "" {
		return "", fmt.Errorf("%w: no projected token path", ErrInvalidCredentials)
	}
	file, err := os.Open(string(path))
	if err != nil {
		return "", fmt.Errorf("module authority: open projected token: %w", err)
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxProjectedToken+1))
	if err != nil {
		return "", fmt.Errorf("module authority: read projected token: %w", err)
	}
	if len(raw) > maxProjectedToken {
		return "", fmt.Errorf("module authority: projected token exceeds %d bytes", maxProjectedToken)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("module authority: projected token is empty")
	}
	return token, nil
}

// maxProjectedToken bounds the token read, at the bound the host's mint
// request declares.
const maxProjectedToken = 64 << 10

// projectedToken reads the current token for one mint.
func (c Credentials) projectedToken() (string, error) {
	if c.Prefix == "" || c.Token == nil {
		return "", ErrInvalidCredentials
	}
	token, err := c.Token.ProjectedToken()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidCredentials, err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("%w: projected token is empty", ErrInvalidCredentials)
	}
	if len(token) > maxProjectedToken {
		return "", fmt.Errorf("%w: projected token exceeds %d bytes", ErrInvalidCredentials, maxProjectedToken)
	}
	return token, nil
}

// ExchangeRequest selects one installed operation binding. SaaS derives the
// audience, scopes, and lifetime from the installation rather than accepting
// them from the caller.
//
// The caller presents its authority one of two ways, and exactly one. Setting
// both, or neither, is ErrInvalidExchange here rather than a round trip: the
// host enforces the same exclusivity as a message-level rule, and a request
// that cannot be authorized should not reach it.
//
//   - Parent, a live capability the caller already holds. The child is
//     attenuated against it and expires with it, so work that outlives the
//     parent cannot be authorized this way at all.
//   - DelegationID, a reference to a host-owned, revocable source delegation.
//     No capability is presented and none needs to still be valid: the host
//     re-checks the delegation and mints a fresh short child from it. This is
//     how work longer than any Work Context stays authorized, and how it
//     renews — by exchanging again against the same reference, after the
//     previous child has already expired.
type ExchangeRequest struct {
	BindingID string
	Parent    string
	Lookup    bool
	// DelegationID names one source delegation, as MintSourceOperationContext
	// reported it. It is an identifier, never a credential: on its own it
	// authorizes nothing, and the caller's own module Work Context still
	// authenticates the call.
	DelegationID string
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
	module    string
	expiresAt time.Time
}

// New binds module authority to the host's two seams. It fails closed, with
// ErrInvalidSeams, on a missing gateway, gateway base URL or internal token,
// on an authority seam that is set but has no address, and on any plaintext seam to a non-loopback address the
// consumer has not asserted protected; and with ErrInvalidCredentials on an
// empty prefix, no token source, or an OrganizationID that is not a canonical
// UUID. It opens no connection and reads no token: the token is read before
// each mint.
func New(seams Seams, credentials Credentials) (*Client, error) {
	if credentials.Prefix == "" || credentials.Token == nil {
		return nil, ErrInvalidCredentials
	}
	if org := credentials.OrganizationID; org != "" {
		// accounts' own rule (module_identity.go, moduleMintOrganization): any
		// other spelling of a UUID is an InvalidArgument at the first mint.
		if id, err := uuid.Parse(org); err != nil || id.String() != org {
			return nil, fmt.Errorf("%w: the organization ID must be a canonical UUID", ErrInvalidCredentials)
		}
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
func (c *Client) ModuleWorkContext(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.moduleWorkContextLocked(ctx)
}

// ExchangeOperation exchanges one immutable installed operation binding on the
// authority endpoint, under exactly one of the request's two authority arms. A
// rejected module capability is refreshed and retried once; neither the parent
// nor the issued child is ever stored.
func (c *Client) ExchangeOperation(ctx context.Context, exchange ExchangeRequest) (string, error) {
	parent := exchange.Parent
	// Exactly one of the two arms, which is the host's message-level rule read
	// back here: a request carrying both presents an authority the host would
	// have to choose between, and one carrying neither presents none at all.
	if exchange.BindingID == "" || (parent == "") == (exchange.DelegationID == "") || (parent != "" && !sealed(parent)) {
		return "", ErrInvalidExchange
	}
	issued, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.ExchangeDelegatedOperationAudience, &v1.ModuleExchangeDelegatedOperationAudienceRequest{
		BindingId:              exchange.BindingID,
		ParentWorkContextToken: parent,
		Lookup:                 exchange.Lookup,
		DelegationId:           exchange.DelegationID,
	})
	if err != nil {
		return "", fmt.Errorf("module authority: exchange operation audience: %w", err)
	}
	if !sealed(issued.GetToken()) {
		return "", fmt.Errorf("%w: malformed exchanged token", ErrInvalidCapability)
	}
	return issued.GetToken(), nil
}

// OperationContext is a Work Context minted with no person present for one
// installed operation binding, with what SaaS reports it asserts.
type OperationContext struct {
	Token       string
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
	projected, err := c.credentials.projectedToken()
	if err != nil {
		return OperationContext{}, err
	}
	var issued operationContextResponse
	if err := c.broker.post(ctx, operationContextPath, projected, operationContextRequest{
		Prefix:         c.credentials.Prefix,
		Binding:        bindingID,
		OrganizationID: c.credentials.OrganizationID,
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

func (c *Client) moduleWorkContextLocked(ctx context.Context) (string, error) {
	if c.module != "" && c.now().Add(c.refreshSkew).Before(c.expiresAt) {
		return c.module, nil
	}
	projected, err := c.credentials.projectedToken()
	if err != nil {
		return "", err
	}
	var issued workContextResponse
	if err := c.broker.post(ctx, workContextPath, projected, workContextRequest{
		Prefix:         c.credentials.Prefix,
		OrganizationID: c.credentials.OrganizationID,
	}, &issued); err != nil {
		return "", fmt.Errorf("module authority: mint module Work Context: %w", brokerRefusal(err))
	}
	expiresAt, err := parseExpiry(issued.ExpiresAt, c.now())
	if err != nil {
		return "", err
	}
	if !sealed(issued.Token) {
		return "", fmt.Errorf("%w: malformed token", ErrInvalidCapability)
	}
	c.module = issued.Token
	c.expiresAt = expiresAt
	return issued.Token, nil
}

// forgetModuleWorkContext drops the cached module capability if it is still
// the one a call was rejected with, so the next caller mints a fresh one. A
// capability another caller already refreshed is kept.
func (c *Client) forgetModuleWorkContext(rejected string) {
	if c.module == rejected {
		c.module = ""
		c.expiresAt = time.Time{}
	}
}
