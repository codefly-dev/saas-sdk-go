package moduleauthority

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	codefly "github.com/codefly-dev/sdk-go/workcontext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// This file fakes the two host seams exactly as module-saas-starter serves
// them, so the client is tested against the host's contract rather than
// against itself:
//
//   - fakeGateway is the auth-gateway's module broker
//     (module/services/auth-gateway/code/gateway_modules.go): the three
//     POST /modules/_* paths, their perimeter (internal token + module
//     secret header), body validation, status mapping, plain-text refusals and
//     JSON answers. Every other path answers 404 "endpoint not exposed", as the
//     real gateway does for every EXPOSURE_INTERNAL procedure — the old bug was
//     a Connect call to exactly such a path.
//   - fakeAuthority is accounts' `authority` gRPC endpoint, a real grpc-go
//     server: the module-exposure check of grpc_auth_interceptor.go (internal
//     token, procedure served) and the module Work Context check of
//     module_capabilities_server.go.

const (
	testInternalToken = "internal-perimeter-token"
	testPrefix        = "documents"
	testSecret        = "projected-secret"
	testTenant        = "11111111-1111-4111-8111-111111111111"
	testPrincipal     = "00000000-0000-4000-8000-00000000beef"
	testOwner         = "44444444-4444-4444-8444-444444444444"
	testDelegation    = "33333333-3333-4333-8333-333333333333"
	exampleSource     = "55555555-5555-4555-8555-555555555555"
)

// The broker's paths as the host serves them, spelled out rather than taken
// from the client's constants, so a wrong constant cannot agree with itself.
const (
	hostWorkContextPath            = "/modules/_work-context"
	hostOperationContextPath       = "/modules/_operation-context"
	hostSourceOperationContextPath = "/modules/_source-operation-context"
)

var moduleCredentials = Credentials{Prefix: testPrefix, Secret: testSecret}

// catalogIdentity mirrors the gateway's validCatalogIdentity closely enough
// for a prefix: one lowercase DNS-ish segment.
var catalogIdentity = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// refusal is a broker answer other than 200.
type refusal struct {
	status int
	body   string
}

type fakeGateway struct {
	server *httptest.Server

	mu sync.Mutex
	// issued counts module Work Contexts minted; the n-th is "module.token<n>".
	issued int
	// valid is the set of module Work Contexts the authority endpoint accepts.
	valid map[string]bool
	// rejectAll makes the authority endpoint reject every module Work Context.
	rejectAll bool
	// calls records every broker path hit; stray records every other path.
	calls []string
	stray []string
	// bodies records each decoded request body, by path.
	bodies map[string][]map[string]string
	// headers records the credential headers of each request.
	headers []http.Header

	// Behaviour knobs, per path.
	refuse   map[string]refusal
	expires  time.Time
	binding  string // overrides the binding answered by the operation mint
	sourceID string // overrides the source answered by the source mint
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	g := &fakeGateway{
		valid:   map[string]bool{},
		bodies:  map[string][]map[string]string{},
		refuse:  map[string]refusal{},
		expires: time.Now().Add(time.Hour),
	}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGateway) BaseURL() string          { return g.server.URL }
func (g *fakeGateway) HTTPClient() *http.Client { return g.server.Client() }

func plainError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, msg)
}

func (rf refusal) write(w http.ResponseWriter) {
	if rf.status >= 300 && rf.status < 400 {
		w.Header().Set("Location", rf.body)
	}
	plainError(w, rf.status, rf.body)
}

func answer(w http.ResponseWriter, body map[string]string) {
	raw, _ := json.Marshal(body)
	w.Header().Set("content-type", "application/json")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (g *fakeGateway) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch r.URL.Path {
	case hostWorkContextPath, hostOperationContextPath, hostSourceOperationContextPath:
	default:
		g.stray = append(g.stray, r.URL.Path)
		plainError(w, http.StatusNotFound, "endpoint not exposed")
		return
	}
	g.calls = append(g.calls, r.URL.Path)
	g.headers = append(g.headers, r.Header.Clone())
	if r.Method != http.MethodPost {
		plainError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Header.Get("X-Codefly-Internal-Token") != testInternalToken {
		plainError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	secret := r.Header.Get("X-Codefly-Module-Secret")
	if secret == "" {
		plainError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var payload map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload); err != nil {
		plainError(w, http.StatusBadRequest, "invalid json")
		return
	}
	g.bodies[r.URL.Path] = append(g.bodies[r.URL.Path], payload)
	if !catalogIdentity.MatchString(payload["prefix"]) {
		plainError(w, http.StatusBadRequest, "invalid prefix")
		return
	}
	// accounts' verdict on the secret: the gateway maps it per path.
	if payload["prefix"] != testPrefix || secret != testSecret {
		plainError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	expiresAt := g.expires.UTC().Format(time.RFC3339)
	switch r.URL.Path {
	case hostWorkContextPath:
		if rf, ok := g.refuse[hostWorkContextPath]; ok {
			rf.write(w)
			return
		}
		g.issued++
		token := fmt.Sprintf("module.token%d", g.issued)
		g.valid[token] = true
		answer(w, map[string]string{"token": token, "expiresAt": expiresAt, "principalId": testPrincipal, "tenant": testTenant})
	case hostOperationContextPath:
		binding := payload["binding"]
		if binding == "" || len(binding) > 128 {
			plainError(w, http.StatusBadRequest, "invalid binding")
			return
		}
		if rf, ok := g.refuse[hostOperationContextPath]; ok {
			rf.write(w)
			return
		}
		if g.binding != "" {
			binding = g.binding
		}
		answer(w, map[string]string{
			"work_context": "operation.token", "expires_at": expiresAt, "principal_id": testPrincipal,
			"tenant": testTenant, "audience": "modelservice", "binding": binding,
		})
	case hostSourceOperationContextPath:
		delegation, source := payload["delegation_id"], payload["source_id"]
		if (delegation == "") == (source == "") || len(delegation) > 64 || len(source) > 64 {
			plainError(w, http.StatusBadRequest, "exactly one of delegation_id or source_id is required")
			return
		}
		if rf, ok := g.refuse[hostSourceOperationContextPath]; ok {
			rf.write(w)
			return
		}
		if g.sourceID != "" {
			source = g.sourceID
		}
		answer(w, map[string]string{
			"work_context": "operation.token", "expires_at": expiresAt, "principal_id": testPrincipal,
			"owner_principal_id": testOwner, "tenant": testTenant, "audience": "runtime",
			"binding": "runtime-admission", "delegation_id": testDelegation, "source_id": source,
		})
	}
}

// revoke makes the authority endpoint reject a module Work Context it issued.
func (g *fakeGateway) revoke(token string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.valid, token)
}

func (g *fakeGateway) accepts(token string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.valid[token] && !g.rejectAll
}

func (g *fakeGateway) snapshot() (calls, stray []string, bodies map[string][]map[string]string, headers []http.Header) {
	g.mu.Lock()
	defer g.mu.Unlock()
	copied := map[string][]map[string]string{}
	for k, v := range g.bodies {
		copied[k] = slices.Clone(v)
	}
	return slices.Clone(g.calls), slices.Clone(g.stray), copied, slices.Clone(g.headers)
}

// authorityCall is one call the fake authority endpoint received.
type authorityCall struct {
	audit         *v1.ModuleEmitAuditEventRequest
	boundary      *v1.VerifyWorkContextRuntimeBoundaryRequest
	authorization []string
	method        string
	internalToken []string
	workContext   []string
	request       *v1.ModuleExchangeDelegatedOperationAudienceRequest
	notice        *v1.ModuleNotifyOrgAdminsRequest
	userNotice    *v1.ModuleNotifyUserRequest
}

// servedOnAuthority is the slice of business.ModuleAuthorityProcedures this
// package calls; Mint* are never among them.
var servedOnAuthority = map[string]bool{
	accountsv1connect.ModuleCapabilitiesServiceEmitAuditEventProcedure:                     true,
	accountsv1connect.ModuleCapabilitiesServiceLookupAuditEventProcedure:                   true,
	accountsv1connect.ModuleCapabilitiesServiceVerifyWorkContextRuntimeBoundaryProcedure:   true,
	accountsv1connect.ModuleCapabilitiesServiceExchangeDelegatedOperationAudienceProcedure: true,
	accountsv1connect.ModuleCapabilitiesServiceExchangeDelegatedReadAudienceProcedure:      true,
	accountsv1connect.ModuleCapabilitiesServiceNotifyOrgAdminsProcedure:                    true,
	accountsv1connect.ModuleCapabilitiesServiceNotifyUserProcedure:                         true,
}

type fakeAuthority struct {
	gateway *fakeGateway
	address string

	mu    sync.Mutex
	calls []authorityCall
}

func newFakeAuthority(t *testing.T, gateway *fakeGateway, creds credentials.TransportCredentials) *fakeAuthority {
	t.Helper()
	a := &fakeAuthority{gateway: gateway}
	options := []grpc.ServerOption{grpc.UnknownServiceHandler(a.handle)}
	if creds != nil {
		options = append(options, grpc.Creds(creds))
	}
	server := grpc.NewServer(options...)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.address = listener.Addr().String()
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return a
}

func (a *fakeAuthority) handle(_ any, stream grpc.ServerStream) error {
	method, _ := grpc.MethodFromServerStream(stream)
	md, _ := metadata.FromIncomingContext(stream.Context())
	call := authorityCall{method: method, authorization: md.Get("authorization"), internalToken: md.Get("x-codefly-internal-token"), workContext: md.Get(codefly.WorkContextHeaderName)}
	// Each procedure's own request message: decoding NotifyOrgAdmins as an
	// exchange request would read its fields as the wrong ones.
	request := &v1.ModuleExchangeDelegatedOperationAudienceRequest{}
	var recvErr error
	switch method {
	case accountsv1connect.ModuleCapabilitiesServiceVerifyWorkContextRuntimeBoundaryProcedure:
		call.boundary = &v1.VerifyWorkContextRuntimeBoundaryRequest{}
		recvErr = stream.RecvMsg(call.boundary)
	case accountsv1connect.ModuleCapabilitiesServiceEmitAuditEventProcedure, accountsv1connect.ModuleCapabilitiesServiceLookupAuditEventProcedure:
		call.audit = &v1.ModuleEmitAuditEventRequest{}
		recvErr = stream.RecvMsg(call.audit)
	case accountsv1connect.ModuleCapabilitiesServiceNotifyOrgAdminsProcedure:
		call.notice = &v1.ModuleNotifyOrgAdminsRequest{}
		recvErr = stream.RecvMsg(call.notice)
	case accountsv1connect.ModuleCapabilitiesServiceNotifyUserProcedure:
		call.userNotice = &v1.ModuleNotifyUserRequest{}
		recvErr = stream.RecvMsg(call.userNotice)
	default:
		recvErr = stream.RecvMsg(request)
		call.request = request
	}
	a.mu.Lock()
	a.calls = append(a.calls, call)
	a.mu.Unlock()
	if recvErr != nil {
		return recvErr
	}
	// grpc_auth_interceptor.go, rpcExposureModule.
	if !servedOnAuthority[method] {
		return status.Error(codes.PermissionDenied, "RPC is not exposed on the module authority endpoint")
	}
	if len(call.internalToken) == 0 || call.internalToken[0] != testInternalToken {
		return status.Error(codes.PermissionDenied, "internal service credential required")
	}
	// exchangeDelegatedAudience / moduleCaller.
	if len(call.workContext) != 1 {
		return status.Error(codes.Unauthenticated, "one module Work Context required")
	}
	if !a.gateway.accepts(call.workContext[0]) {
		return status.Error(codes.Unauthenticated, "module work context is not a valid capability")
	}
	if call.boundary != nil {
		switch call.boundary.ForwardedWorkContextToken {
		case "denied.token":
			return status.Error(codes.PermissionDenied, "boundary refused")
		case "missing-tenant.token":
			return stream.SendMsg(&v1.VerifyWorkContextRuntimeBoundaryResponse{BoundaryId: exampleSource})
		case "missing-boundary.token":
			return stream.SendMsg(&v1.VerifyWorkContextRuntimeBoundaryResponse{TenantId: testTenant})
		}
		return stream.SendMsg(&v1.VerifyWorkContextRuntimeBoundaryResponse{TenantId: testTenant, BoundaryId: exampleSource})
	}
	if call.audit != nil {
		switch call.audit.GetIdempotencyKey() {
		case "conflict":
			return status.Error(codes.FailedPrecondition, "audit intent conflict")
		case "unavailable":
			return status.Error(codes.Unavailable, "audit storage unavailable")
		}
		if method == accountsv1connect.ModuleCapabilitiesServiceEmitAuditEventProcedure {
			return stream.SendMsg(&emptypb.Empty{})
		}
		eventID := "event-1"
		if call.audit.GetIdempotencyKey() == "missing" {
			eventID = ""
		}
		return stream.SendMsg(&v1.ModuleLookupAuditEventResponse{EventId: eventID})
	}
	if call.notice != nil {
		// ModuleNotifyOrgAdmins: the host resolves the administrators and
		// answers only whether any received the notice.
		if call.notice.GetTenant() == "" || call.notice.GetCategory() == "" {
			return status.Error(codes.InvalidArgument, "tenant and category required")
		}
		if call.notice.GetType() == "sync" {
			return status.Error(codes.InvalidArgument, "invalid notification type")
		}
		return stream.SendMsg(&v1.ModuleNotifyOrgAdminsResponse{Delivered: true})
	}
	if n := call.userNotice; n != nil {
		// ModuleNotifyUser: a known type, a member recipient, and category
		// policy — an optional category the recipient switched off is a
		// success that was not delivered.
		if n.GetTenant() == "" || n.GetUserId() == "" || n.GetCategory() == "" {
			return status.Error(codes.InvalidArgument, "tenant, user and category required")
		}
		if n.GetType() == "sync" {
			return status.Error(codes.InvalidArgument, "invalid notification type")
		}
		if n.GetUserId() == nonMember {
			return status.Error(codes.PermissionDenied, "user is not a member of tenant")
		}
		return stream.SendMsg(&v1.ModuleNotifyUserResponse{NotificationId: "n-1", Delivered: n.GetCategory() != "marketing"})
	}
	if request.GetBindingId() == "" || request.GetParentWorkContextToken() == "" {
		return status.Error(codes.InvalidArgument, "binding and parent required")
	}
	return stream.SendMsg(&v1.IssuedWorkContext{Token: "child.token"})
}

func (a *fakeAuthority) snapshot() []authorityCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

// host is one fake host: both seams, sharing the module Work Contexts the
// broker issued.
type host struct {
	gateway   *fakeGateway
	authority *fakeAuthority
}

func newHost(t *testing.T) host {
	t.Helper()
	gateway := newFakeGateway(t)
	return host{gateway: gateway, authority: newFakeAuthority(t, gateway, nil)}
}

func (h host) seams() Seams {
	return Seams{
		Gateway:       h.gateway,
		Authority:     Authority{Address: h.authority.address},
		InternalToken: testInternalToken,
	}
}

func (h host) client(t *testing.T, credentials Credentials) *Client {
	t.Helper()
	client, err := New(h.seams(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// assertNoStrayPaths holds every test to the regression this package was
// rewritten for: nothing but the broker's three paths ever reaches the
// gateway.
func (h host) assertNoStrayPaths(t *testing.T) {
	t.Helper()
	if _, stray, _, _ := h.gateway.snapshot(); len(stray) != 0 {
		t.Fatalf("the client sent non-broker paths to the gateway: %v", stray)
	}
}

func token(t *testing.T, encoded string) codefly.WorkContextToken {
	t.Helper()
	parsed, err := codefly.ParseWorkContextToken(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
