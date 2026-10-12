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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codefly-dev/core/wool"
	corework "github.com/codefly-dev/core/workcontext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// This file fakes the two host seams as module-saas-starter v0.0.94 serves
// them, so the client is tested against the host's contract rather than
// against itself:
//
//   - fakeGateway is the auth-gateway's module broker
//     (module/services/auth-gateway/code/gateway_modules.go): the three
//     POST /modules/_* paths, their perimeter (internal token + projected
//     ServiceAccount bearer), body validation, status mapping, plain-text refusals and
//     JSON answers. Every other path answers 404 "endpoint not exposed", as the
//     real gateway does for every EXPOSURE_INTERNAL procedure — the old bug was
//     a Connect call to exactly such a path.
//   - fakeAuthority is accounts' `authority` gRPC endpoint, a real grpc-go
//     server: the module-exposure check of grpc_auth_interceptor.go (internal
//     token, procedure served) and the module Work Context check of
//     module_capabilities_server.go.
//
// fakeGateway is the edge-case double; the contract itself is held by
// conformance/hostgateway, which mints through the host's REAL gateway
// handlers at the tag in SOURCE.txt. The fake follows the real gateway's
// perimeter, prefix rule and check order (internal token, then exactly one
// Bearer, then body, prefix and binding/source validation, then accounts'
// verdict). Where it still differs, it differs on purpose:
//
//   - D2: it decodes the body into map[string]string and records every key, so
//     an extra or misnamed field is visible to an exact-body assertion; the
//     real gateway decodes into a struct and drops unknown keys silently.
//   - D5: a test may inject any refusal on any path. The real gateway never
//     answers 403 (or 412) on /modules/_work-context — accounts'
//     PermissionDenied is a 401 there — so no test here injects one.
//   - D6: accounts' Unauthenticated and FailedPrecondition on the work-context
//     mint (a 502 from the real gateway) are not modelled; only injected
//     statuses are.
//   - D7: it accepts one internal token; the real gateway also accepts the
//     previous one during rotation.
//   - D8: refusal bodies are whatever a test injects; the real gateway sends
//     fixed strings and ErrorInfo reasons. The client never reads them.
//
// D1 (the legacy X-Codefly-Module-Secret header beside a valid bearer: the
// fake used to refuse it, the real gateway ignores it), D3 (the prefix
// pattern) and D4 (the token verdict before binding/source validation) are
// aligned with the real gateway. That the client never sends the legacy header
// is asserted on the request itself (assertProjectedBearer), not left to the
// fake's verdict.

const (
	testInternalToken  = "internal-perimeter-token"
	testPrefix         = "documents"
	testProjectedToken = "projected-sa-token"
	testTenant         = "11111111-1111-4111-8111-111111111111"
	testPrincipal      = "00000000-0000-4000-8000-00000000beef"
	testOwner          = "44444444-4444-4444-8444-444444444444"
	testDelegation     = "33333333-3333-4333-8333-333333333333"
	exampleSource      = "55555555-5555-4555-8555-555555555555"
	testBindingID      = "binding-example-1"
)

// The broker's paths as the host serves them, spelled out rather than taken
// from the client's constants, so a wrong constant cannot agree with itself.
const (
	hostWorkContextPath            = "/modules/_work-context"
	hostOperationContextPath       = "/modules/_operation-context"
	hostSourceOperationContextPath = "/modules/_source-operation-context"
)

var moduleCredentials = Credentials{Prefix: testPrefix, Token: staticToken(testProjectedToken)}

// staticToken is a test-only ProjectedTokenSource that answers one value.
type staticToken string

func (s staticToken) ProjectedToken() (string, error) { return string(s), nil }

type failingToken struct{ err error }

func (f failingToken) ProjectedToken() (string, error) { return "", f.err }

// catalogIdentity is the gateway's validCatalogIdentity (routing_artifact.go
// at v0.0.94): one DNS label, at most 63 characters.
var catalogIdentity = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

func validCatalogIdentity(value string) bool {
	return len(value) <= 63 && catalogIdentity.MatchString(value)
}

// refusal is a broker answer other than 200.
type refusal struct {
	status int
	body   string
}

type fakeGateway struct {
	server *httptest.Server

	mu sync.Mutex
	// issued counts module Work Contexts minted; the n-th is
	// sealedToken("module.token<n>").
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
	refuse         map[string]refusal
	expires        time.Time
	binding        string // overrides the binding answered by the operation mint
	sourceID       string // overrides the source answered by the source mint
	projectedToken string // token currently accepted by the simulated accounts verifier
	foreign        bool   // answers every mint with a token core does not decode
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	g := &fakeGateway{
		valid:          map[string]bool{},
		bodies:         map[string][]map[string]string{},
		refuse:         map[string]refusal{},
		expires:        time.Now().Add(time.Hour),
		projectedToken: testProjectedToken,
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
	// The real gateway reads no X-Codefly-Module-Secret header at all, so
	// neither does this one: it is neither refused nor accepted on its own.
	// Exactly one Bearer token, trimmed and bounded as gateway_modules.go
	// lines 112–122 at v0.0.94 (the literal limit is independent of the SDK).
	authorization := r.Header.Values("Authorization")
	if len(authorization) != 1 || !strings.HasPrefix(authorization[0], "Bearer ") {
		plainError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	projected := strings.TrimSpace(strings.TrimPrefix(authorization[0], "Bearer "))
	if projected == "" || len(projected) > 64*1024 {
		plainError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var payload map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload); err != nil {
		plainError(w, http.StatusBadRequest, "invalid json")
		return
	}
	g.bodies[r.URL.Path] = append(g.bodies[r.URL.Path], payload)
	if !validCatalogIdentity(payload["prefix"]) {
		plainError(w, http.StatusBadRequest, "invalid prefix")
		return
	}
	// The gateway validates each path's own fields before it calls accounts.
	switch r.URL.Path {
	case hostOperationContextPath:
		if binding := payload["binding"]; binding == "" || len(binding) > 128 {
			plainError(w, http.StatusBadRequest, "invalid binding")
			return
		}
	case hostSourceOperationContextPath:
		delegation, source := payload["delegation_id"], payload["source_id"]
		if (delegation == "") == (source == "") || len(delegation) > 64 || len(source) > 64 {
			plainError(w, http.StatusBadRequest, "exactly one of delegation_id or source_id is required")
			return
		}
	}
	// accounts' verdict on the token, which the gateway maps to 401 on every
	// path (PermissionDenied on the work-context mint, Unauthenticated on the
	// operation mints).
	if payload["prefix"] != testPrefix || projected != g.projectedToken {
		plainError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	expiresAt := g.expires.UTC().Format(time.RFC3339)
	operation := sealedToken("operation.token")
	if g.foreign {
		operation = corework.FixtureForeignToken()
	}
	switch r.URL.Path {
	case hostWorkContextPath:
		if rf, ok := g.refuse[hostWorkContextPath]; ok {
			rf.write(w)
			return
		}
		g.issued++
		token := sealedToken(fmt.Sprintf("module.token%d", g.issued))
		g.valid[token] = true
		if g.foreign {
			token = corework.FixtureForeignToken()
		}
		answer(w, map[string]string{"token": token, "expiresAt": expiresAt, "principalId": testPrincipal, "tenant": testTenant})
	case hostOperationContextPath:
		binding := payload["binding"]
		if rf, ok := g.refuse[hostOperationContextPath]; ok {
			rf.write(w)
			return
		}
		if g.binding != "" {
			binding = g.binding
		}
		answer(w, map[string]string{
			"work_context": operation, "expires_at": expiresAt, "principal_id": testPrincipal,
			"tenant": testTenant, "audience": "modelservice", "binding": binding,
		})
	case hostSourceOperationContextPath:
		source := payload["source_id"]
		if rf, ok := g.refuse[hostSourceOperationContextPath]; ok {
			rf.write(w)
			return
		}
		if g.sourceID != "" {
			source = g.sourceID
		}
		answer(w, map[string]string{
			"work_context": operation, "expires_at": expiresAt, "principal_id": testPrincipal,
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
	declaration    *v1.ModuleDeclareAuditEventTypesRequest
	moduleSecret   []string
	audit          *v1.ModuleEmitAuditEventRequest
	installation   *v1.ModuleCurrentInstallationRequest
	authorization  []string
	artifact       *v1.ModuleExecutableArtifactRequest
	revokeArtifact *v1.ModuleRevokeExecutableArtifactRequest
	method         string
	internalToken  []string
	workContext    []string
	request        *v1.ModuleExchangeDelegatedOperationAudienceRequest
	notice         *v1.ModuleNotifyOrgAdminsRequest
	userNotice     *v1.ModuleNotifyUserRequest
	// authorityQuestion is one live-authority question, so a test can assert
	// the capability travelled with it and that the identifiers are the
	// capability's own.
	authorityQuestion *v1.ModuleResolveWorkContextAuthorityRequest
	journal        *v1.ModuleReadJournalRequest
}

// servedOnAuthority is the slice of business.ModuleAuthorityProcedures this
// package calls; Mint* are never among them.
var servedOnAuthority = map[string]bool{
	accountsv1connect.ModuleCapabilitiesServiceDeclareAuditEventTypesProcedure:             true,
	accountsv1connect.ModuleCapabilitiesServiceEmitAuditEventProcedure:                     true,
	accountsv1connect.ModuleCapabilitiesServiceGetCurrentInstallationProcedure:             true,
	accountsv1connect.ModuleCapabilitiesServiceApproveExecutableArtifactProcedure:          true,
	accountsv1connect.ModuleCapabilitiesServiceAuthorizeExecutableArtifactProcedure:        true,
	accountsv1connect.ModuleCapabilitiesServiceRevokeExecutableArtifactProcedure:           true,
	accountsv1connect.ModuleCapabilitiesServiceExchangeDelegatedOperationAudienceProcedure: true,
	accountsv1connect.ModuleCapabilitiesServiceExchangeDelegatedReadAudienceProcedure:      true,
	accountsv1connect.ModuleCapabilitiesServiceNotifyOrgAdminsProcedure:                    true,
	accountsv1connect.ModuleCapabilitiesServiceNotifyUserProcedure:                         true,
	accountsv1connect.ModuleCapabilitiesServiceResolveWorkContextAuthorityProcedure:        true,
	// ReadJournal is not in the v0.0.94 host: it is the wire contract the next
	// host release is to serve (see SOURCE.txt), faked here as that contract
	// states it.
	accountsv1connect.ModuleCapabilitiesServiceReadJournalProcedure: true,
}

type fakeAuthority struct {
	gateway *fakeGateway
	address string
	// declarationError is the host admission verdict, configured before a call.
	declarationError error
	// foreign answers an exchange with a token core does not decode.
	foreign bool
	// authorityRefusal is the host's verdict on a live-authority question,
	// configured before a call. nil answers the question.
	authorityRefusal error
	// authority is the live state this host answers, by question kind.
	authority authorityState
	// journal is what the fake host has committed, in seq order. A test sets it
	// before a call; the fake serves it the way the contract states.
	journal []*v1.ModuleJournalEntry
	// journalNext, when set, overrides next_after_seq, to model a host that
	// holds the resume point back of the last entry it returned.
	journalNext *int64
	// crossTenant is whether the fake host treats this caller as a cross-tenant
	// module principal. ReadJournal is refused to any other.
	crossTenant bool

	mu    sync.Mutex
	calls []authorityCall
}

func newFakeAuthority(t *testing.T, gateway *fakeGateway, creds credentials.TransportCredentials) *fakeAuthority {
	t.Helper()
	a := &fakeAuthority{gateway: gateway, crossTenant: true}
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
	call := authorityCall{moduleSecret: md.Get("x-codefly-module-secret"), authorization: md.Get("authorization"), method: method, internalToken: md.Get("x-codefly-internal-token"), workContext: md.Get(wool.WorkContextHeader)}
	// Each procedure's own request message: decoding NotifyOrgAdmins as an
	// exchange request would read its fields as the wrong ones.
	request := &v1.ModuleExchangeDelegatedOperationAudienceRequest{}
	var recvErr error
	switch method {
	case accountsv1connect.ModuleCapabilitiesServiceDeclareAuditEventTypesProcedure:
		call.declaration = &v1.ModuleDeclareAuditEventTypesRequest{}
		recvErr = stream.RecvMsg(call.declaration)
	case accountsv1connect.ModuleCapabilitiesServiceEmitAuditEventProcedure:
		call.audit = &v1.ModuleEmitAuditEventRequest{}
		recvErr = stream.RecvMsg(call.audit)
	case accountsv1connect.ModuleCapabilitiesServiceGetCurrentInstallationProcedure:
		call.installation = &v1.ModuleCurrentInstallationRequest{}
		recvErr = stream.RecvMsg(call.installation)
	case accountsv1connect.ModuleCapabilitiesServiceApproveExecutableArtifactProcedure, accountsv1connect.ModuleCapabilitiesServiceAuthorizeExecutableArtifactProcedure:
		call.artifact = &v1.ModuleExecutableArtifactRequest{}
		recvErr = stream.RecvMsg(call.artifact)
	case accountsv1connect.ModuleCapabilitiesServiceRevokeExecutableArtifactProcedure:
		call.revokeArtifact = &v1.ModuleRevokeExecutableArtifactRequest{}
		recvErr = stream.RecvMsg(call.revokeArtifact)
	case accountsv1connect.ModuleCapabilitiesServiceNotifyOrgAdminsProcedure:
		call.notice = &v1.ModuleNotifyOrgAdminsRequest{}
		recvErr = stream.RecvMsg(call.notice)
	case accountsv1connect.ModuleCapabilitiesServiceNotifyUserProcedure:
		call.userNotice = &v1.ModuleNotifyUserRequest{}
		recvErr = stream.RecvMsg(call.userNotice)
	case accountsv1connect.ModuleCapabilitiesServiceResolveWorkContextAuthorityProcedure:
		call.authorityQuestion = &v1.ModuleResolveWorkContextAuthorityRequest{}
		recvErr = stream.RecvMsg(call.authorityQuestion)
	case accountsv1connect.ModuleCapabilitiesServiceReadJournalProcedure:
		call.journal = &v1.ModuleReadJournalRequest{}
		recvErr = stream.RecvMsg(call.journal)
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
	if j := call.journal; j != nil {
		return a.serveJournal(stream, j)
	}
	if call.declaration != nil {
		if a.declarationError != nil {
			return a.declarationError
		}
		return stream.SendMsg(&v1.ModuleDeclareAuditEventTypesResponse{})
	}
	if call.audit != nil {
		switch call.audit.GetIdempotencyKey() {
		case "actor-unresolved":
			return status.Error(codes.FailedPrecondition, "audit actor unresolved")
		case "unavailable":
			return status.Error(codes.Unavailable, "audit storage unavailable")
		case "deadline":
			return status.Error(codes.DeadlineExceeded, "audit deadline exceeded")
		case "internal":
			return status.Error(codes.Internal, "audit commit failed")
		}
		return stream.SendMsg(&emptypb.Empty{})
	}
	if call.installation != nil {
		if call.installation.ParentWorkContextToken == sealedToken("denied.token") {
			return status.Error(codes.PermissionDenied, "current authority denied")
		}
		out := &v1.ModuleCurrentInstallationResponse{InstallationId: call.installation.InstallationId, TenantId: testTenant, TargetId: testOwner, BindingId: testBindingID}
		switch call.installation.ParentWorkContextToken {
		case sealedToken("wrong-id.token"):
			out.InstallationId = testOwner
		case sealedToken("missing-tenant.token"):
			out.TenantId = ""
		case sealedToken("empty-target.token"):
			out.TargetId = ""
		case sealedToken("invalid-target.token"):
			out.TargetId = "invalid"
		case sealedToken("empty-binding.token"):
			out.BindingId = ""
		case sealedToken("invalid-binding.token"):
			out.BindingId = "   "
		}
		return stream.SendMsg(out)
	}
	if call.artifact != nil {
		if call.artifact.PolicyId == "denied" {
			return status.Error(codes.PermissionDenied, "exact artifact approval required")
		}
		revision := call.artifact.Identity.ExpectedRevision
		if call.artifact.PolicyId == "mismatched-receipt" {
			revision--
		}
		return stream.SendMsg(&v1.ModuleExecutableArtifactResponse{SchemaVersion: "host.executable-artifact/v1", QualifiedName: "host/approved-artifacts/11111111-1111-4111-8111-111111111111", Digest: "sha256:" + strings.Repeat("a", 64), ExpectedRevision: revision})
	}
	if call.revokeArtifact != nil {
		return stream.SendMsg(&v1.ModuleExecutableArtifactResponse{SchemaVersion: "host.executable-artifact/v1", QualifiedName: "host/approved-artifacts/" + call.revokeArtifact.ApprovalId, Digest: "sha256:" + strings.Repeat("a", 64), ExpectedRevision: 0})
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
	if question := call.authorityQuestion; question != nil {
		// ResolveWorkContextAuthority: one live-authority answer, or the
		// host's refusal.
		if a.authorityRefusal != nil {
			return a.authorityRefusal
		}
		answer, err := a.answerAuthority(question)
		if err != nil {
			return err
		}
		return stream.SendMsg(answer)
	}
	// The host's own message-level rule: a binding, and exactly one of the two
	// authority arms. Stated here so a client that sent both, or neither, is
	// caught by the fake the way the real surface would catch it.
	if request.GetBindingId() == "" ||
		(request.GetParentWorkContextToken() == "") == (request.GetDelegationId() == "") {
		return status.Error(codes.InvalidArgument, "binding and exactly one of parent or delegation required")
	}
	if a.foreign {
		return stream.SendMsg(&v1.IssuedWorkContext{Token: corework.FixtureForeignToken()})
	}
	return stream.SendMsg(&v1.IssuedWorkContext{Token: sealedToken("child.token")})
}

// serveJournal answers ReadJournal as the wire contract states it: refused to a
// caller that is not a cross-tenant module principal, entries with a seq
// greater than after_seq of the requested types, at most limit of them (zero
// meaning 100), and a resume point.
func (a *fakeAuthority) serveJournal(stream grpc.ServerStream, req *v1.ModuleReadJournalRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.crossTenant {
		return status.Error(codes.PermissionDenied, "journal is served to cross-tenant module principals only")
	}
	if req.GetAfterSeq() < 0 || req.GetLimit() < 0 || req.GetLimit() > 500 || len(req.GetTypes()) == 0 {
		return status.Error(codes.InvalidArgument, "after_seq >= 0, limit 0..500 and at least one type required")
	}
	limit := int(req.GetLimit())
	if limit == 0 {
		limit = 100
	}
	out := &v1.ModuleReadJournalResponse{NextAfterSeq: req.GetAfterSeq()}
	for _, e := range a.journal {
		if e.GetSeq() <= req.GetAfterSeq() || !slices.Contains(req.GetTypes(), e.GetType()) {
			continue
		}
		if len(out.Entries) == limit {
			break
		}
		out.Entries = append(out.Entries, e)
		out.NextAfterSeq = e.GetSeq()
	}
	if a.journalNext != nil {
		out.NextAfterSeq = *a.journalNext
	}
	return stream.SendMsg(out)
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

// authorityState is the live authority the fake host answers a verifier's
// questions from — the issuer's side of
// ModuleCapabilitiesService/ResolveWorkContextAuthority as
// module-saas-starter serves it.
//
// It is a fake of the ANSWERS and never of the client's translation: every
// state the wire can carry is settable, including the UNSPECIFIED a host that
// said nothing would send, so the client's refusals are exercised rather than
// assumed.
type authorityState struct {
	sealState            v1.WorkContextSealState
	installationRevision uint64
	// installationID overrides the installation the answer is ABOUT, so a
	// client that fails to notice an answer about something else is caught.
	installationID string
	principalState v1.WorkContextPrincipalState
	epoch          uint64
	principalID    string
	buildState     v1.WorkContextApprovedBuildState
	imageDigest    string
	incarnation    uint64
	bindingState   v1.WorkContextOperationBindingState
	binding        *v1.WorkContextOperationBindingAnswer
	revision       uint64
	ownerPrincipal string
	grantState     v1.WorkContextApprovalGrantState
	emptyAnswer    bool
	mismatchedArm  bool
}

// answerAuthority renders one answer for one question.
func (a *fakeAuthority) answerAuthority(
	question *v1.ModuleResolveWorkContextAuthorityRequest,
) (*v1.ModuleResolveWorkContextAuthorityResponse, error) {
	if question == nil {
		return nil, status.Error(codes.InvalidArgument, "no question")
	}
	state := a.authority
	if state.emptyAnswer {
		return &v1.ModuleResolveWorkContextAuthorityResponse{}, nil
	}
	if state.mismatchedArm {
		// A host answering a different question than the one asked. The client
		// must refuse rather than read it.
		return &v1.ModuleResolveWorkContextAuthorityResponse{
			Answer: &v1.ModuleResolveWorkContextAuthorityResponse_AuthorizationRevision{
				AuthorizationRevision: &v1.WorkContextAuthorizationRevisionAnswer{Revision: 1},
			},
		}, nil
	}
	switch asked := question.GetQuestion().(type) {
	case *v1.ModuleResolveWorkContextAuthorityRequest_Seal:
		installation := asked.Seal.GetInstallationId()
		if state.installationID != "" {
			installation = state.installationID
		}
		return &v1.ModuleResolveWorkContextAuthorityResponse{
			Answer: &v1.ModuleResolveWorkContextAuthorityResponse_Seal{
				Seal: &v1.WorkContextSealAnswer{
					State: state.sealState, InstallationId: installation,
					InstallationRevision: state.installationRevision,
				},
			},
		}, nil
	case *v1.ModuleResolveWorkContextAuthorityRequest_PrincipalEpoch:
		principal := asked.PrincipalEpoch.GetPrincipalId()
		if state.principalID != "" {
			principal = state.principalID
		}
		return &v1.ModuleResolveWorkContextAuthorityResponse{
			Answer: &v1.ModuleResolveWorkContextAuthorityResponse_PrincipalEpoch{
				PrincipalEpoch: &v1.WorkContextPrincipalEpochAnswer{
					State: state.principalState, PrincipalId: principal, Epoch: state.epoch,
				},
			},
		}, nil
	case *v1.ModuleResolveWorkContextAuthorityRequest_ApprovedBuild:
		principal := asked.ApprovedBuild.GetPrincipalId()
		if state.principalID != "" {
			principal = state.principalID
		}
		return &v1.ModuleResolveWorkContextAuthorityResponse{
			Answer: &v1.ModuleResolveWorkContextAuthorityResponse_ApprovedBuild{
				ApprovedBuild: &v1.WorkContextApprovedBuildAnswer{
					State: state.buildState, PrincipalId: principal,
					ImageDigest: state.imageDigest, BuildIncarnation: state.incarnation,
				},
			},
		}, nil
	case *v1.ModuleResolveWorkContextAuthorityRequest_OperationBinding:
		answer := state.binding
		if answer == nil {
			answer = &v1.WorkContextOperationBindingAnswer{
				State: state.bindingState, BindingId: asked.OperationBinding.GetBindingId(),
			}
		}
		return &v1.ModuleResolveWorkContextAuthorityResponse{
			Answer: &v1.ModuleResolveWorkContextAuthorityResponse_OperationBinding{OperationBinding: answer},
		}, nil
	case *v1.ModuleResolveWorkContextAuthorityRequest_AuthorizationRevision:
		owner := asked.AuthorizationRevision.GetOwnerPrincipalId()
		if state.ownerPrincipal != "" {
			owner = state.ownerPrincipal
		}
		return &v1.ModuleResolveWorkContextAuthorityResponse{
			Answer: &v1.ModuleResolveWorkContextAuthorityResponse_AuthorizationRevision{
				AuthorizationRevision: &v1.WorkContextAuthorizationRevisionAnswer{
					OwnerPrincipalId: owner, Revision: state.revision,
				},
			},
		}, nil
	case *v1.ModuleResolveWorkContextAuthorityRequest_ApprovalGrant:
		return &v1.ModuleResolveWorkContextAuthorityResponse{
			Answer: &v1.ModuleResolveWorkContextAuthorityResponse_ApprovalGrant{
				ApprovalGrant: &v1.WorkContextApprovalGrantAnswer{
					State: state.grantState, GrantId: asked.ApprovalGrant.GetGrantId(),
				},
			},
		}, nil
	}
	return nil, status.Error(codes.InvalidArgument, "unknown question")
}
