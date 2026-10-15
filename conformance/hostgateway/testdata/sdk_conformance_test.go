package main

// SDK conformance against the host's REAL auth-gateway.
//
// This file belongs to codefly-dev/saas-sdk-go, not to the host. run.sh
// compiles it into module-saas-starter's auth-gateway package (package main,
// module/services/auth-gateway/code at the ref SOURCE.txt records) with
// `go test -overlay`, so the host checkout stays byte-for-byte unmodified, and
// drives the SDK's real moduleauthority client against the host's real
// workloadListener(Gateway) over httptest, as the native host does.
//
// Only accounts is faked, on a real gRPC listener and exactly the way the
// host's own gateway tests fake it (gateway_modules_test.go,
// gateway_operation_context_test.go, gateway_source_operation_context_test.go):
// accounts' real mints need a Kubernetes TokenReview and a database.
//
// The host exports no test seam for this, so the file binds to these
// unexported names of the host package, and to nothing else of it:
//
//   - newGatewayHarness (gateway_test.go): the host's own gateway under test;
//   - (*Gateway).authz, (*ExtAuthz).backendConn: where the harness's accounts
//     connection is set, as the host's own tests set it;
//   - (*ExtAuthz).internalToken: the perimeter credential the harness chose;
//   - workloadListener, authenticatedWorkloadListener and the host
//     testWaypoint/testPresenter fixtures (workload_listener.go and
//     workload_presenter_test.go);
//   - the accounts types in auth-gateway/pkg/gen/saas/accounts/v1.
//
// The faked accounts answers Work Contexts in core's encoding
// (core/workcontext, signed with core's public fixture key), because the SDK
// carries only tokens core decodes. The host's REAL accounts at a ref before
// module-saas-starter#1093 mints the pre-cutover JSON payload instead, which
// the SDK refuses after the gateway relays it:
// TestSDKConformancePreCutoverTokenRefused holds that, through these handlers.
//
// A host change to any of them fails this file's compilation: red, never a
// silent pass.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	accountsv1 "auth-gateway/pkg/gen/saas/accounts/v1"

	sdkcorework "github.com/codefly-dev/core/workcontext"
	"github.com/codefly-dev/saas-sdk-go/moduleauthority"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	sdkPrefix     = "documents"
	sdkOrg        = "66666666-6666-4666-8666-666666666666"
	sdkSource     = "33333333-3333-4333-8333-333333333333"
	sdkDelegation = "22222222-2222-4222-8222-222222222222"
	sdkTenant     = "11111111-1111-4111-8111-111111111111"
	sdkPrincipal  = "00000000-0000-4000-8000-00000000beef"
	sdkOwner      = "44444444-4444-4444-8444-444444444444"
	sdkBinding    = "model"

	// accountsErrorDomain is the ErrorInfo domain of accounts' delegation
	// refusals, spelled out rather than taken from the host's constant: it is
	// the wire contract between accounts and the gateway.
	accountsErrorDomain = "accounts.saas.codefly.dev"

	mintWork      = "MintModuleWorkContext"
	mintOperation = "MintModuleOperationContext"
	mintSource    = "MintSourceOperationContext"
)

var mintPaths = map[string]string{
	mintWork:      "/modules/_work-context",
	mintOperation: "/modules/_operation-context",
	mintSource:    "/modules/_source-operation-context",
}

// accountsCall is one mint the gateway forwarded to the accounts fake.
type accountsCall struct {
	method   string
	internal []string
	md       metadata.MD
	work     *accountsv1.ModuleMintWorkContextRequest
	op       *accountsv1.ModuleMintOperationContextRequest
	src      *accountsv1.ModuleMintSourceOperationContextRequest
}

type fakeAccounts struct {
	mu    sync.Mutex
	calls []accountsCall
	// refuse is accounts' answer per method, instead of a Work Context.
	refuse map[string]error
	// preCutover answers every mint with a pre-cutover JSON-payload token,
	// which is what the host's real accounts mints before #1093.
	preCutover bool
}

// token is the Work Context accounts answers for method: sdkSealed(method),
// or the pre-cutover encoding.
func (a *fakeAccounts) token(method string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.preCutover {
		return sdkcorework.FixtureForeignToken()
	}
	return sdkSealed(method)
}

var sdkSealedTokens sync.Map

// sdkSealed is a Work Context in core's encoding, named by its task ID and
// signed with core's conformance fixture key — whose private half is public,
// so nothing trusts it. The SDK only asks core whether a token is sealed; it
// verifies none, so this is exactly as good as accounts' own signature here.
func sdkSealed(name string) string {
	if token, ok := sdkSealedTokens.Load(name); ok {
		return token.(string)
	}
	ctx := context.Background()
	_, key := sdkcorework.FixtureKeyPair()
	seals := sdkcorework.FixtureSeals()
	digest, incarnation, err := seals.ApprovedBuild(ctx, sdkcorework.FixturePrincipal)
	if err != nil {
		panic(err)
	}
	authority := &sdkcorework.Authority{
		Issuer: sdkcorework.FixtureIssuer, KeyID: sdkcorework.FixtureKeyID, Key: key,
		Revisions: sdkcorework.FixtureRevisions(), Seals: seals,
	}
	token, _, err := authority.Start(ctx, sdkcorework.StartInput{
		Execution:          sdkcorework.Execution{ImageDigest: digest, BuildIncarnation: incarnation},
		TenantID:           sdkcorework.FixtureTenant,
		OwnerPrincipalID:   sdkcorework.FixturePrincipal,
		OwnerPrincipalKind: "service",
		OrganizationID:     sdkcorework.FixtureOrganization,
		TaskID:             name,
		Audience:           sdkcorework.FixtureAudience,
		InstallationID:     sdkcorework.FixtureInstallation,
		TTL:                time.Hour,
	})
	if err != nil {
		panic(err)
	}
	stored, _ := sdkSealedTokens.LoadOrStore(name, token)
	return stored.(string)
}

func (a *fakeAccounts) record(ctx context.Context, call accountsCall) error {
	md, _ := metadata.FromIncomingContext(ctx)
	call.md = md
	call.internal = md.Get("x-codefly-internal-token")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, call)
	return a.refuse[call.method]
}

func (a *fakeAccounts) snapshot() []accountsCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

// edgeRequest is one HTTP request as it reached the gateway.
type edgeRequest struct {
	path   string
	header http.Header
}

// realGateway is the host's gateway behind httptest, with what reached it at
// the edge and what it forwarded to accounts.
type realGateway struct {
	server        *httptest.Server
	gateway       *Gateway
	internalToken string
	accounts      *fakeAccounts

	mu   sync.Mutex
	edge []edgeRequest
}

func (g *realGateway) BaseURL() string          { return g.server.URL }
func (g *realGateway) HTTPClient() *http.Client { return g.server.Client() }

func (g *realGateway) edgeRequests() []edgeRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.edge)
}

func unary(name string, handle func(context.Context, func(any) error) (any, error)) grpc.MethodDesc {
	return grpc.MethodDesc{MethodName: name, Handler: func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		return handle(ctx, dec)
	}}
}

func newRealGateway(t *testing.T) *realGateway {
	t.Helper()
	gw, _, _, _ := newGatewayHarness(t)
	accounts := &fakeAccounts{refuse: map[string]error{}}
	expires := timestamppb.New(time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second))

	server := grpc.NewServer()
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "saas.accounts.v1.ModuleCapabilitiesService",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			unary(mintWork, func(ctx context.Context, dec func(any) error) (any, error) {
				req := &accountsv1.ModuleMintWorkContextRequest{}
				if err := dec(req); err != nil {
					return nil, err
				}
				if err := accounts.record(ctx, accountsCall{method: mintWork, work: req}); err != nil {
					return nil, err
				}
				return &accountsv1.ModuleMintWorkContextResponse{
					Token: accounts.token(mintWork), ExpiresAt: expires, PrincipalId: sdkPrincipal, Tenant: sdkTenant,
				}, nil
			}),
			unary(mintOperation, func(ctx context.Context, dec func(any) error) (any, error) {
				req := &accountsv1.ModuleMintOperationContextRequest{}
				if err := dec(req); err != nil {
					return nil, err
				}
				if err := accounts.record(ctx, accountsCall{method: mintOperation, op: req}); err != nil {
					return nil, err
				}
				return &accountsv1.ModuleMintOperationContextResponse{
					Token: accounts.token(mintOperation), ExpiresAt: expires, PrincipalId: sdkPrincipal, Tenant: sdkTenant,
					Audience: "modelservice", Binding: req.GetBinding(),
				}, nil
			}),
			unary(mintSource, func(ctx context.Context, dec func(any) error) (any, error) {
				req := &accountsv1.ModuleMintSourceOperationContextRequest{}
				if err := dec(req); err != nil {
					return nil, err
				}
				if err := accounts.record(ctx, accountsCall{method: mintSource, src: req}); err != nil {
					return nil, err
				}
				return &accountsv1.ModuleMintSourceOperationContextResponse{
					Token: accounts.token(mintSource), ExpiresAt: expires, PrincipalId: sdkPrincipal, Tenant: sdkTenant,
					Audience: "ingest", Binding: "source-sync", DelegationId: sdkDelegation, SourceId: req.GetSourceId(),
					OwnerPrincipalId: sdkOwner,
				}, nil
			}),
		},
		Metadata: "saas/accounts/v1/module_registration.proto",
	}, accounts)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	gw.authz.backendConn = conn

	g := &realGateway{gateway: gw, internalToken: gw.authz.internalToken, accounts: accounts}
	handler := workloadListener(gw)
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.edge = append(g.edge, edgeRequest{path: r.URL.Path, header: r.Header.Clone()})
		g.mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(g.server.Close)
	if g.internalToken == "" {
		t.Fatal("the host harness configured no internal token")
	}
	return g
}

// rotatingToken is a projected token source whose value a test changes
// between mints, counting each read.
type rotatingToken struct {
	mu    sync.Mutex
	token string
	reads int
}

func (s *rotatingToken) ProjectedToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return s.token, nil
}

func (s *rotatingToken) set(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
}

func newSDKClient(t *testing.T, g *realGateway, token moduleauthority.ProjectedTokenSource, org string) *moduleauthority.Client {
	t.Helper()
	client, err := moduleauthority.New(
		moduleauthority.Seams{Gateway: g, InternalToken: g.internalToken},
		moduleauthority.Credentials{Prefix: sdkPrefix, Token: token, OrganizationID: org},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func mint(ctx context.Context, client *moduleauthority.Client, method string) error {
	var err error
	switch method {
	case mintWork:
		_, err = client.ModuleWorkContext(ctx)
	case mintOperation:
		_, err = client.MintModuleOperationContext(ctx, sdkBinding)
	case mintSource:
		_, err = client.MintSourceOperationContext(ctx, sdkSource)
	}
	return err
}

// assertEdge holds the request the SDK sent: exactly one projected bearer,
// the internal token, and no removed module-secret header.
func assertEdge(t *testing.T, g *realGateway, request edgeRequest, path, token string) {
	t.Helper()
	if request.path != path {
		t.Fatalf("SDK called %s, want %s", request.path, path)
	}
	if got := request.header.Values("Authorization"); !slices.Equal(got, []string{"Bearer " + token}) {
		t.Fatalf("SDK sent Authorization %d time(s), want exactly one Bearer carrying the current projected token", len(got))
	}
	if got := request.header.Values("X-Codefly-Internal-Token"); !slices.Equal(got, []string{g.internalToken}) {
		t.Fatal("SDK did not send the internal token exactly once")
	}
	if len(request.header.Values("X-Codefly-Module-Secret")) != 0 {
		t.Fatal("SDK sent the removed X-Codefly-Module-Secret header")
	}
}

// assertForwarded holds what the gateway forwarded to accounts: its own
// internal token, and no credential header of the module's.
func assertForwarded(t *testing.T, g *realGateway, call accountsCall, method string) {
	t.Helper()
	if call.method != method {
		t.Fatalf("accounts received %s, want %s", call.method, method)
	}
	if !slices.Equal(call.internal, []string{g.internalToken}) {
		t.Fatalf("accounts received internal token %d time(s), want the gateway's own once", len(call.internal))
	}
	if len(call.md.Get("x-codefly-module-secret")) != 0 || len(call.md.Get("authorization")) != 0 {
		t.Fatal("the gateway forwarded a module credential header to accounts")
	}
}

// Every mint, through the real handlers: accepted, with exactly the fields
// accounts needs, and the token read afresh for each.
func TestSDKConformanceThreeMints(t *testing.T) {
	g := newRealGateway(t)
	token := &rotatingToken{token: "projected-1"}
	client := newSDKClient(t, g, token, sdkOrg)
	ctx := t.Context()

	work, err := client.ModuleWorkContext(ctx)
	if err != nil {
		t.Fatalf("work-context mint: %v", err)
	}
	if work != sdkSealed(mintWork) {
		t.Fatalf("work context = %q", work)
	}

	token.set("projected-2")
	op, err := client.MintModuleOperationContext(ctx, sdkBinding)
	if err != nil {
		t.Fatalf("operation-context mint: %v", err)
	}
	if op.Token != sdkSealed(mintOperation) || op.BindingID != sdkBinding || op.Audience != "modelservice" ||
		op.Tenant != sdkTenant || op.PrincipalID != sdkPrincipal || !op.ExpiresAt.After(time.Now()) {
		t.Fatalf("operation context = %+v", op)
	}

	token.set("projected-3")
	src, err := client.MintSourceOperationContext(ctx, sdkSource)
	if err != nil {
		t.Fatalf("source-operation-context mint: %v", err)
	}
	if src.Token != sdkSealed(mintSource) || src.SourceID != sdkSource || src.DelegationID != sdkDelegation ||
		src.OwnerPrincipalID != sdkOwner || src.Tenant != sdkTenant || src.Audience != "ingest" || src.BindingID != "source-sync" {
		t.Fatalf("source operation context = %+v", src)
	}

	edge := g.edgeRequests()
	calls := g.accounts.snapshot()
	if len(edge) != 3 || len(calls) != 3 || token.reads != 3 {
		t.Fatalf("edge requests = %d, accounts calls = %d, token reads = %d; want 3 each", len(edge), len(calls), token.reads)
	}
	assertEdge(t, g, edge[0], mintPaths[mintWork], "projected-1")
	assertEdge(t, g, edge[1], mintPaths[mintOperation], "projected-2")
	assertEdge(t, g, edge[2], mintPaths[mintSource], "projected-3")

	assertForwarded(t, g, calls[0], mintWork)
	if w := calls[0].work; w.GetPrefix() != sdkPrefix || w.GetServiceAccountToken() != "projected-1" || w.GetOrganizationId() != sdkOrg {
		t.Fatalf("accounts received work mint prefix=%q token=%q organization_id=%q", w.GetPrefix(), w.GetServiceAccountToken(), w.GetOrganizationId())
	}
	assertForwarded(t, g, calls[1], mintOperation)
	if o := calls[1].op; o.GetPrefix() != sdkPrefix || o.GetServiceAccountToken() != "projected-2" ||
		o.GetBinding() != sdkBinding || o.GetOrganizationId() != sdkOrg {
		t.Fatalf("accounts received operation mint prefix=%q token=%q binding=%q organization_id=%q",
			o.GetPrefix(), o.GetServiceAccountToken(), o.GetBinding(), o.GetOrganizationId())
	}
	assertForwarded(t, g, calls[2], mintSource)
	if s := calls[2].src; s.GetPrefix() != sdkPrefix || s.GetServiceAccountToken() != "projected-3" ||
		s.GetSourceId() != sdkSource || s.GetDelegationId() != "" {
		t.Fatalf("accounts received source mint prefix=%q token=%q source_id=%q delegation_id=%q",
			s.GetPrefix(), s.GetServiceAccountToken(), s.GetSourceId(), s.GetDelegationId())
	}
}

// The pre-cutover JSON-payload token — what the host's real accounts mints
// before module-saas-starter#1093 — is relayed by the real gateway on every
// mint, and refused by the SDK with ErrInvalidCapability: this SDK carries
// only Work Contexts core decodes. The mint reached accounts once per path.
func TestSDKConformancePreCutoverTokenRefused(t *testing.T) {
	g := newRealGateway(t)
	g.accounts.mu.Lock()
	g.accounts.preCutover = true
	g.accounts.mu.Unlock()
	client := newSDKClient(t, g, &rotatingToken{token: "projected-1"}, sdkOrg)
	for _, method := range []string{mintWork, mintOperation, mintSource} {
		if err := mint(t.Context(), client, method); !errors.Is(err, moduleauthority.ErrInvalidCapability) {
			t.Fatalf("%s answered a pre-cutover token: SDK error %v, want ErrInvalidCapability", method, err)
		}
	}
	if calls := g.accounts.snapshot(); len(calls) != 3 {
		t.Fatalf("accounts calls = %d, want one per mint", len(calls))
	}
}

// A tenant-pinned module names no organization: none is forwarded.
func TestSDKConformanceNoOrganization(t *testing.T) {
	g := newRealGateway(t)
	client := newSDKClient(t, g, &rotatingToken{token: "projected-1"}, "")
	for _, method := range []string{mintWork, mintOperation} {
		if err := mint(t.Context(), client, method); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	calls := g.accounts.snapshot()
	if len(calls) != 2 || calls[0].work.GetOrganizationId() != "" || calls[1].op.GetOrganizationId() != "" {
		t.Fatalf("accounts received an organization the module did not name: %d calls", len(calls))
	}
}

// The kubelet rotates the projected file under a running process; the next
// mint presents the rotated, trimmed token.
func TestSDKConformanceRotatedProjectedFile(t *testing.T) {
	g := newRealGateway(t)
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("rotation-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := newSDKClient(t, g, moduleauthority.ProjectedTokenFile(path), "")
	if err := mint(t.Context(), client, mintOperation); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("  rotation-b  "), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mint(t.Context(), client, mintSource); err != nil {
		t.Fatal(err)
	}
	calls := g.accounts.snapshot()
	if len(calls) != 2 || calls[0].op.GetServiceAccountToken() != "rotation-a" || calls[1].src.GetServiceAccountToken() != "rotation-b" {
		t.Fatal("accounts did not receive each mint's current projected token")
	}
}

// The SDK's 64 KiB bound is the gateway's: the largest token both accept,
// and one byte more both refuse.
func TestSDKConformanceTokenBound(t *testing.T) {
	g := newRealGateway(t)
	largest := strings.Repeat("a", 64*1024)
	if err := mint(t.Context(), newSDKClient(t, g, &rotatingToken{token: largest}, ""), mintOperation); err != nil {
		t.Fatalf("a 64 KiB token: %v", err)
	}
	if calls := g.accounts.snapshot(); len(calls) != 1 || calls[0].op.GetServiceAccountToken() != largest {
		t.Fatal("accounts did not receive the 64 KiB token")
	}
	err := mint(t.Context(), newSDKClient(t, g, &rotatingToken{token: largest + "a"}, ""), mintOperation)
	if !errors.Is(err, moduleauthority.ErrInvalidCredentials) || len(g.edgeRequests()) != 1 {
		t.Fatalf("the SDK sent a token over 64 KiB (%v)", err)
	}
	if code := post(t, g, mintPaths[mintOperation], `{"prefix":"documents","binding":"model"}`, func(h http.Header) {
		h.Set("Authorization", "Bearer "+largest+"a")
	}); code != http.StatusUnauthorized {
		t.Fatalf("the gateway answered %d to a token over 64 KiB, want 401", code)
	}
	if len(g.accounts.snapshot()) != 1 {
		t.Fatal("the gateway forwarded a token over 64 KiB")
	}
}

func refusal(code codes.Code, reason string) error {
	st := status.New(code, "refused")
	if reason != "" {
		var err error
		if st, err = st.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: accountsErrorDomain}); err != nil {
			panic(err)
		}
	}
	return st.Err()
}

var sdkSentinels = []error{
	moduleauthority.ErrInvalidCredentials,
	moduleauthority.ErrPermissionDenied,
	moduleauthority.ErrExecutionUnestablished,
	moduleauthority.ErrDelegationMissing,
	moduleauthority.ErrDelegationInvalid,
	moduleauthority.ErrDelegationRevoked,
}

// Each refusal, from accounts' code through the real gateway's status to the
// SDK's error: the status is preserved, and the SDK types it with exactly one
// sentinel (or none).
func TestSDKConformanceRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		method   string
		accounts error
		status   int
		sentinel error
	}{
		// Work-context mint: accounts answers PermissionDenied for a module it
		// cannot prove — and for an organization the module may not mint for
		// (none named by a shared module, another named by a pinned one).
		{"work unproven or wrong organization", mintWork, refusal(codes.PermissionDenied, ""), 401, moduleauthority.ErrInvalidCredentials},
		{"work execution unestablished", mintWork, refusal(codes.Unavailable, ""), 503, moduleauthority.ErrExecutionUnestablished},
		{"work invalid request", mintWork, refusal(codes.InvalidArgument, ""), 400, nil},
		{"work unauthenticated", mintWork, refusal(codes.Unauthenticated, ""), 401, moduleauthority.ErrInvalidCredentials},
		{"work failed precondition", mintWork, refusal(codes.FailedPrecondition, ""), 412, moduleauthority.ErrNoHostStateToMintFor},

		{"operation unproven", mintOperation, refusal(codes.Unauthenticated, ""), 401, moduleauthority.ErrInvalidCredentials},
		// The same organization refusal is a 403 on the operation mint.
		{"operation denied or wrong organization", mintOperation, refusal(codes.PermissionDenied, ""), 403, moduleauthority.ErrPermissionDenied},
		{"operation invalid request", mintOperation, refusal(codes.InvalidArgument, ""), 400, nil},
		{"operation execution unestablished", mintOperation, refusal(codes.Unavailable, ""), 503, moduleauthority.ErrExecutionUnestablished},

		{"source unproven", mintSource, refusal(codes.Unauthenticated, ""), 401, moduleauthority.ErrInvalidCredentials},
		{"source delegation missing", mintSource, refusal(codes.FailedPrecondition, "DELEGATION_MISSING"), 412, moduleauthority.ErrDelegationMissing},
		{"source other precondition", mintSource, refusal(codes.FailedPrecondition, "SOMETHING_ELSE"), 502, nil},
		{"source delegation revoked", mintSource, refusal(codes.PermissionDenied, "DELEGATION_REVOKED"), 403, moduleauthority.ErrDelegationInvalid},
		{"source delegation invalid", mintSource, refusal(codes.PermissionDenied, "DELEGATION_INVALID"), 403, moduleauthority.ErrDelegationInvalid},
		{"source forbidden without a reason", mintSource, refusal(codes.PermissionDenied, ""), 403, moduleauthority.ErrDelegationInvalid},
		{"source execution unestablished", mintSource, refusal(codes.Unavailable, ""), 503, moduleauthority.ErrExecutionUnestablished},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRealGateway(t)
			g.accounts.refuse[tc.method] = tc.accounts
			err := mint(t.Context(), newSDKClient(t, g, &rotatingToken{token: "projected-refused"}, sdkOrg), tc.method)
			var be *moduleauthority.BrokerError
			if !errors.As(err, &be) || be.StatusCode != tc.status {
				t.Fatalf("accounts %v: SDK error %v, want a BrokerError with HTTP %d", status.Code(tc.accounts), err, tc.status)
			}
			for _, sentinel := range sdkSentinels {
				if errors.Is(err, sentinel) != (sentinel == tc.sentinel) {
					t.Fatalf("HTTP %d: SDK error %v, want only %v", tc.status, err, tc.sentinel)
				}
			}
			if strings.Contains(err.Error(), "projected-refused") {
				t.Fatal("the SDK error carries the projected token")
			}
			if calls := g.accounts.snapshot(); len(calls) != 1 || calls[0].method != tc.method {
				t.Fatalf("accounts calls = %d, want one %s", len(calls), tc.method)
			}
		})
	}
}

// Operation/source mints retain the perimeter gate. The work mint on the
// workload listener authenticates the projected credential instead (#1120).
func TestSDKConformanceWrongInternalToken(t *testing.T) {
	g := newRealGateway(t)
	client, err := moduleauthority.New(
		moduleauthority.Seams{Gateway: g, InternalToken: "not-the-internal-token"},
		moduleauthority.Credentials{Prefix: sdkPrefix, Token: &rotatingToken{token: "projected-1"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := mint(t.Context(), client, mintWork); err != nil {
		t.Fatalf("workload work mint with a valid projected credential: %v", err)
	}
	for _, method := range []string{mintOperation, mintSource} {
		var be *moduleauthority.BrokerError
		if err := mint(t.Context(), client, method); !errors.Is(err, moduleauthority.ErrInvalidCredentials) || !errors.As(err, &be) || be.StatusCode != 401 {
			t.Fatalf("%s: %v, want ErrInvalidCredentials with HTTP 401", method, err)
		}
	}
	calls := g.accounts.snapshot()
	if len(calls) != 1 || calls[0].method != mintWork {
		t.Fatalf("accounts calls = %v, want only the workload work mint", calls)
	}
	assertForwarded(t, g, calls[0], mintWork)
}

// The protected workload listener authenticates the waypoint's assertion,
// strips caller-supplied presenter headers and forwards its own gateway proof.
// These identities are the host's public test fixtures, never production values.
func TestSDKConformanceWorkloadPresenter(t *testing.T) {
	g := newRealGateway(t)
	handler, err := authenticatedWorkloadListener(g.gateway, testWaypoint)
	if err != nil {
		t.Fatal(err)
	}
	protected := httptest.NewServer(handler)
	t.Cleanup(protected.Close)
	for name, values := range map[string][]string{
		"missing":   nil,
		"forged":    {"By=" + testPresenter + ";URI=" + testPresenter},
		"duplicate": {"By=" + testWaypoint + ";URI=" + testPresenter, "By=" + testWaypoint + ";URI=" + testPresenter},
		"valid":     {"By=" + testWaypoint + ";URI=" + testPresenter},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(g.accounts.snapshot())
			r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, protected.URL+mintPaths[mintWork], strings.NewReader(mintBodies[mintWork]))
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Authorization", "Bearer projected-1")
			r.Header["X-Forwarded-Client-Cert"] = values
			r.Header.Set(workloadPresenterHeader, "forged")
			r.Header.Set("X-Codefly-Gateway-Token", "forged")
			response, err := protected.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			calls := g.accounts.snapshot()
			if name != "valid" {
				if response.StatusCode != http.StatusUnauthorized || len(calls) != before {
					t.Fatalf("invalid presenter: status=%d accounts=%d", response.StatusCode, len(calls)-before)
				}
				return
			}
			if response.StatusCode != http.StatusOK || len(calls) != before+1 {
				t.Fatalf("valid presenter: status=%d accounts=%d", response.StatusCode, len(calls)-before)
			}
			call := calls[len(calls)-1]
			assertForwarded(t, g, call, mintWork)
			if !slices.Equal(call.md.Get(workloadPresenterHeader), []string{testPresenter}) || !slices.Equal(call.md.Get("x-codefly-gateway-token"), []string{g.gateway.authz.gatewayToken}) {
				t.Fatal("gateway did not forward its authenticated presenter and proof")
			}
		})
	}
	public := httptest.NewServer(g.gateway)
	t.Cleanup(public.Close)
	before := len(g.accounts.snapshot())
	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, public.URL+mintPaths[mintWork], strings.NewReader(mintBodies[mintWork]))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer projected-1")
	r.Header.Set("X-Codefly-Internal-Token", g.internalToken)
	r.Header.Set("X-Forwarded-Client-Cert", "By="+testWaypoint+";URI="+testPresenter)
	response, err := public.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound || len(g.accounts.snapshot()) != before {
		t.Fatalf("public listener served a workload mint: status=%d", response.StatusCode)
	}
}

func post(t *testing.T, g *realGateway, path, body string, headers func(http.Header)) int {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Codefly-Internal-Token", g.internalToken)
	headers(request.Header)
	response, err := g.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

var mintBodies = map[string]string{
	mintWork:      `{"prefix":"documents"}`,
	mintOperation: `{"prefix":"documents","binding":"model"}`,
	mintSource:    `{"prefix":"documents","source_id":"` + sdkSource + `"}`,
}

// The wire shape the SDK replaced, and malformed bearers, are refused at the
// gateway on every mint path; accounts never hears of them.
func TestSDKConformanceRemovedAndMalformedCredentials(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers func(http.Header)
	}{
		{"legacy secret header", func(h http.Header) { h.Set("X-Codefly-Module-Secret", "removed-module-secret") }},
		{"missing bearer", func(http.Header) {}},
		{"duplicate bearer", func(h http.Header) {
			h.Add("Authorization", "Bearer projected-a")
			h.Add("Authorization", "Bearer projected-b")
		}},
		{"non-Bearer scheme", func(h http.Header) { h.Set("Authorization", "Secret removed-module-secret") }},
		{"lowercase scheme", func(h http.Header) { h.Set("Authorization", "bearer projected-1") }},
		{"empty bearer", func(h http.Header) { h.Set("Authorization", "Bearer  ") }},
	} {
		for _, method := range []string{mintWork, mintOperation, mintSource} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				g := newRealGateway(t)
				if code := post(t, g, mintPaths[method], mintBodies[method], tc.headers); code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", code)
				}
				if calls := g.accounts.snapshot(); len(calls) != 0 {
					t.Fatal("accounts was called")
				}
			})
		}
	}
}

// The three answers moduleauthority's fake gateway copies from this one
// (host_fake_test.go, D1, D3, D4). They are not the SDK's contract, but if
// the host changes them the fake no longer follows the real gateway and must
// be realigned: TestHostFakeFollowsTheRealGatewayOrder holds the fake to the
// same table.
func TestSDKConformanceFakeGatewayStillFollowsThisOne(t *testing.T) {
	for _, tc := range []struct {
		name     string
		method   string
		body     string
		bearer   string
		legacy   bool
		accounts error
		want     int
	}{
		{"D1 legacy header beside a bearer is ignored", mintWork, mintBodies[mintWork], "projected-1", true, nil, http.StatusOK},
		{"D3 trailing hyphen prefix", mintWork, `{"prefix":"documents-"}`, "projected-1", false, nil, http.StatusBadRequest},
		{"D4 binding before token", mintOperation, `{"prefix":"documents","binding":""}`, "not-the-token", false, refusal(codes.Unauthenticated, ""), http.StatusBadRequest},
		{"D4 source before token", mintSource, `{"prefix":"documents"}`, "not-the-token", false, refusal(codes.Unauthenticated, ""), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRealGateway(t)
			if tc.accounts != nil {
				g.accounts.refuse[tc.method] = tc.accounts
			}
			code := post(t, g, mintPaths[tc.method], tc.body, func(h http.Header) {
				h.Set("Authorization", "Bearer "+tc.bearer)
				if tc.legacy {
					h.Set("X-Codefly-Module-Secret", "removed-module-secret")
				}
			})
			if code != tc.want {
				t.Fatalf("status = %d, want %d", code, tc.want)
			}
			calls := g.accounts.snapshot()
			if tc.want != http.StatusOK {
				if len(calls) != 0 {
					t.Fatal("accounts was called for a request the gateway refused")
				}
				return
			}
			if len(calls) != 1 {
				t.Fatalf("accounts calls = %d, want 1", len(calls))
			}
			assertForwarded(t, g, calls[0], tc.method)
		})
	}
}
