package moduleauthority

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	corework "github.com/codefly-dev/core/workcontext"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// The live authority behind a capability presented TO this module.
//
// A module that receives a Work Context is its callee, and a callee verifies at
// full strength. The checks a trust root alone answers — encoding, signature,
// issuer, audience, window, shape — are a forwarding hop's; the rest is the
// ISSUER'S live state: the installation a capability is sealed to and its
// revision, the epoch of every principal in its chain, the build the exercising
// principal runs, the operation binding it exercises, and the authorization
// revision. A module that stops at the hop's checks accepts a capability whose
// installation was revoked or whose principal's epoch has moved, for as long as
// that capability lives.
//
// This file is the CLIENT of those answers and nothing more. It verifies
// nothing: it holds no key, reads no signature, and makes no decision about a
// capability. Verification is core's, reached through sdk-go/workcontext's
// PinnedVerifier, and these three sources are what that verifier is given:
//
//	sources, err := client.WorkContextSources(moduleauthority.WorkContextSourceSettings{
//	    Capability: presented, // the capability this module is verifying
//	})
//	verifier, err := workcontext.NewVerifier(workcontext.VerifierSettings{
//	    Issuer: issuer, Audience: audience,
//	    Seals: sources, Revisions: sources, Grants: sources,
//	    Replay: replayStore, // the consumer's own, durable and shared
//	}, keySet)
//
// That split is the rule this package already keeps for a token: the module is
// a holder and a sender, never a verifier. A source is a client of the issuer,
// which is this package's whole job; the verifier stays in sdk-go/workcontext.

// ErrInvalidAuthorityQuestion: a source was asked something it will not send —
// an empty or non-canonical principal, installation or binding identifier, or a
// source built with an incomplete selector. Nothing is sent, because a
// malformed question cannot be answered safely: a host that resolved something
// adjacent would be compared against the capability as though it were the
// answer.
var ErrInvalidAuthorityQuestion = errors.New("module authority: invalid live-authority question")

// ErrUnreadableAuthorityAnswer: the host answered something this client will not
// read — an answer on another arm than the question, an answer about another
// principal, installation or binding, or a state this client does not
// understand. It is a REFUSAL and never a permissive default: an answer that
// cannot be read is not evidence that a capability is still good.
var ErrUnreadableAuthorityAnswer = errors.New("module authority: unreadable live-authority answer")

// A HOST REFUSAL AND A HOST OUTAGE ARE DIFFERENT ANSWERS, and a caller acts on
// them differently: `CodeFailedPrecondition` means the issuer decided and the
// authority the question asks about has changed — the capability is revoked, so
// mint again and do not retry — while `CodeUnavailable` means the lookup failed
// and a retry may succeed. They arrive as CONNECT codes, read with
// `connect.CodeOf`; grpc-go's `status.Code` answers Unknown for them, because
// this client calls over Connect.
//
// ErrNoApprovalGrant: the host records no approval grant under the id a
// capability's grant hop claims. Core's GrantSource has no absence sentinel,
// deliberately — a capability claiming an approval the issuer never made is not
// a credential — so this is an error, and a verifier refuses on it.
var ErrNoApprovalGrant = errors.New("module authority: the issuer records no such approval grant")

// AuthorityCacheBound is the longest a resolved answer may be served from
// memory, and the ceiling WorkContextSources enforces on a configured one.
//
// A cache delays a revocation by at most its bound, so the bound is the
// security parameter. It is short enough to be a rounding error against the
// 15-minute capability ceiling and long enough to collapse the several
// questions one verification asks about the same principal into one round trip.
//
// Only POSITIVE answers are cached. A refusal is never cached: re-asking a
// question that refused costs one call and keeps a transient failure from
// becoming a sticky one.
const AuthorityCacheBound = 2 * time.Second

// defaultAuthorityCacheEntries bounds how many answers are held, so a module
// verifying capabilities for many principals cannot grow this without limit.
const defaultAuthorityCacheEntries = 2048

// WorkContextSourceSettings is everything a source set is, besides the client.
//
// There is exactly one required field, and that is the design: the host answers
// only about the capability presented with each question, so the capability is
// the whole selector. The tenant, the audience and the owner used to be fields
// here; the host now reads all three off the capability it authenticates, which
// is what makes them its own statement rather than a caller's label.
type WorkContextSourceSettings struct {
	// Capability is the Work Context this module is verifying, exactly as it
	// was presented to it. It travels with every question.
	//
	// It is shape-checked here with core's structural reader and nothing more:
	// this package holds Work Contexts and never verifies one. The host
	// authenticates it, requires it to be addressed to this module and to its
	// tenant, and refuses a question about anything it does not name — so a
	// claim read off it here can only narrow what is asked, never widen what is
	// answered.
	Capability string

	// CacheBound is how long a positive answer may be served from memory. Zero
	// means AuthorityCacheBound; a value above it is refused rather than
	// clamped, because a clamp would serve a posture the caller did not choose
	// and never tell them.
	CacheBound time.Duration

	// CacheEntries bounds the cache. Zero means defaultAuthorityCacheEntries. A
	// negative value disables caching, which is the strongest posture: every
	// question is one call.
	CacheEntries int

	// Now is the clock the cache reads. nil means time.Now.
	Now func() time.Time
}

// WorkContextSources answers the live authority behind ONE capability presented
// to this module.
//
// It implements core's SealSource, RevisionSource and GrantSource, and is handed
// to a verifier as all three.
type WorkContextSources struct {
	client   *Client
	settings WorkContextSourceSettings
	// owner is the capability's own owner, read off it with core's structural
	// reader. The host checks it against the same capability, so a
	// misreading here refuses rather than misanswers.
	owner string

	mu     sync.Mutex
	cached map[string]cachedAuthorityAnswer
}

type cachedAuthorityAnswer struct {
	answer *v1.ModuleResolveWorkContextAuthorityResponse
	until  time.Time
}

var (
	_ corework.SealSource     = (*WorkContextSources)(nil)
	_ corework.RevisionSource = (*WorkContextSources)(nil)
	_ corework.GrantSource    = (*WorkContextSources)(nil)
)

// WorkContextSources builds the three sources for one presented capability, or
// refuses the settings by name.
//
// It is per capability because the host answers per capability. Building one is
// cheap — no call is made here — and a module verifying a stream of messages
// carrying the same capability keeps one and reuses it.
func (c *Client) WorkContextSources(settings WorkContextSourceSettings) (*WorkContextSources, error) {
	if c == nil {
		return nil, ErrInvalidAuthorityQuestion
	}
	if !sealed(settings.Capability) {
		return nil, fmt.Errorf(
			"%w: the capability is not structurally a sealed Work Context in core's encoding",
			ErrInvalidAuthorityQuestion)
	}
	decoded, err := corework.Decode(settings.Capability)
	if err != nil {
		return nil, fmt.Errorf("%w: the capability could not be read", ErrInvalidAuthorityQuestion)
	}
	owner := decoded.Context().GetOwnerPrincipalId()
	if owner == "" {
		return nil, fmt.Errorf("%w: the capability names no owner", ErrInvalidAuthorityQuestion)
	}
	if settings.CacheBound > AuthorityCacheBound {
		return nil, fmt.Errorf(
			"%w: a cache bound of %s delays a revocation by that long; the ceiling is %s",
			ErrInvalidAuthorityQuestion, settings.CacheBound, AuthorityCacheBound)
	}
	if settings.CacheBound == 0 {
		settings.CacheBound = AuthorityCacheBound
	}
	if settings.CacheEntries == 0 {
		settings.CacheEntries = defaultAuthorityCacheEntries
	}
	return &WorkContextSources{client: c, settings: settings, owner: owner}, nil
}

func (s *WorkContextSources) now() time.Time {
	if s.settings.Now == nil {
		return time.Now()
	}
	return s.settings.Now()
}

// ask sends one question, serving a positive answer from the cache while it is
// inside the bound.
//
// key is the question's identity. A cache keyed on anything less specific than
// the whole question would answer one question with another's answer, which is
// the failure a cache must not have.
func (s *WorkContextSources) ask(
	ctx context.Context, key string, ask func(*v1.ModuleResolveWorkContextAuthorityRequest),
) (*v1.ModuleResolveWorkContextAuthorityResponse, error) {
	if cached, ok := s.load(key); ok {
		return cached, nil
	}
	request := &v1.ModuleResolveWorkContextAuthorityRequest{WorkContext: s.settings.Capability}
	ask(request)
	answer, err := callAsModule(ctx, s.client,
		accountsv1connect.ModuleCapabilitiesServiceClient.ResolveWorkContextAuthority, request)
	if err != nil {
		return nil, err
	}
	if answer == nil {
		return nil, ErrUnreadableAuthorityAnswer
	}
	s.store(key, answer)
	return answer, nil
}

func (s *WorkContextSources) load(key string) (*v1.ModuleResolveWorkContextAuthorityResponse, bool) {
	if s.settings.CacheEntries < 0 {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, held := s.cached[key]
	if !held || !s.now().Before(entry.until) {
		delete(s.cached, key)
		return nil, false
	}
	return entry.answer, true
}

func (s *WorkContextSources) store(key string, answer *v1.ModuleResolveWorkContextAuthorityResponse) {
	if s.settings.CacheEntries < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached == nil {
		s.cached = make(map[string]cachedAuthorityAnswer, 8)
	}
	// At the bound, the whole cache is dropped rather than one entry evicted by
	// age: this holds a handful of answers for a few seconds, and a dropped
	// cache costs one extra call while a wrong eviction policy costs a reader's
	// understanding of what is cached.
	if len(s.cached) >= s.settings.CacheEntries {
		s.cached = make(map[string]cachedAuthorityAnswer, 8)
	}
	s.cached[key] = cachedAuthorityAnswer{answer: answer, until: s.now().Add(s.settings.CacheBound)}
}

// Seal answers the live installation one principal holds, for core's SealSource.
//
// The installation in the answer is compared against the one asked about before
// anything else is read: a host that resolved a different installation would
// make every comparison core then makes meaningless, and core checks this too.
// Checking it here as well is not duplication — it is this client refusing to
// carry an answer it cannot attribute.
func (s *WorkContextSources) Seal(
	ctx context.Context, principalID, installationID string,
) (corework.Seal, error) {
	if principalID == "" || installationID == "" {
		return corework.Seal{}, fmt.Errorf("%w: seal", ErrInvalidAuthorityQuestion)
	}
	answer, err := s.ask(ctx, "seal\x00"+principalID+"\x00"+installationID,
		func(request *v1.ModuleResolveWorkContextAuthorityRequest) {
			request.Question = &v1.ModuleResolveWorkContextAuthorityRequest_Seal{
				Seal: &v1.WorkContextSealQuestion{
					PrincipalId: principalID, InstallationId: installationID,
				},
			}
		})
	if err != nil {
		return corework.Seal{}, err
	}
	seal := answer.GetSeal()
	if seal == nil || seal.GetInstallationId() != installationID {
		return corework.Seal{}, fmt.Errorf("%w: seal", ErrUnreadableAuthorityAnswer)
	}
	switch seal.GetState() {
	case v1.WorkContextSealState_WORK_CONTEXT_SEAL_STATE_HELD:
		return corework.Seal{
			InstallationID:       seal.GetInstallationId(),
			InstallationRevision: seal.GetInstallationRevision(),
		}, nil
	case v1.WorkContextSealState_WORK_CONTEXT_SEAL_STATE_UNHELD:
		// Core's own sentinel, which its verifier turns into a refusal of the
		// capability rather than into an outage.
		return corework.Seal{}, corework.ErrNoSeal
	default:
		return corework.Seal{}, fmt.Errorf("%w: seal state", ErrUnreadableAuthorityAnswer)
	}
}

// PrincipalEpoch answers one principal's live epoch. It is asked of the task
// owner and of every actor hop, which is what makes a delegated actor revocable
// on its own.
func (s *WorkContextSources) PrincipalEpoch(ctx context.Context, principalID string) (uint64, error) {
	if principalID == "" {
		return 0, fmt.Errorf("%w: principal epoch", ErrInvalidAuthorityQuestion)
	}
	answer, err := s.ask(ctx, "epoch\x00"+principalID,
		func(request *v1.ModuleResolveWorkContextAuthorityRequest) {
			request.Question = &v1.ModuleResolveWorkContextAuthorityRequest_PrincipalEpoch{
				PrincipalEpoch: &v1.WorkContextPrincipalEpochQuestion{PrincipalId: principalID},
			}
		})
	if err != nil {
		return 0, err
	}
	epoch := answer.GetPrincipalEpoch()
	if epoch == nil || epoch.GetPrincipalId() != principalID {
		return 0, fmt.Errorf("%w: principal epoch", ErrUnreadableAuthorityAnswer)
	}
	switch epoch.GetState() {
	case v1.WorkContextPrincipalState_WORK_CONTEXT_PRINCIPAL_STATE_KNOWN:
		return epoch.GetEpoch(), nil
	case v1.WorkContextPrincipalState_WORK_CONTEXT_PRINCIPAL_STATE_UNKNOWN:
		// A principal the issuer does not hold. Core turns this into a refusal;
		// an epoch of zero would compare equal to a real epoch of zero and so
		// would pass for exactly the capabilities that claim nothing.
		return 0, corework.ErrNoSeal
	default:
		return 0, fmt.Errorf("%w: principal state", ErrUnreadableAuthorityAnswer)
	}
}

// ApprovedBuild answers what execution the issuer APPROVES for one principal —
// never what a workload presents, which is the caller's attestation and the
// other half of the comparison.
func (s *WorkContextSources) ApprovedBuild(
	ctx context.Context, principalID string,
) (string, uint64, error) {
	if principalID == "" {
		return "", 0, fmt.Errorf("%w: approved build", ErrInvalidAuthorityQuestion)
	}
	answer, err := s.ask(ctx, "build\x00"+principalID,
		func(request *v1.ModuleResolveWorkContextAuthorityRequest) {
			request.Question = &v1.ModuleResolveWorkContextAuthorityRequest_ApprovedBuild{
				ApprovedBuild: &v1.WorkContextApprovedBuildQuestion{PrincipalId: principalID},
			}
		})
	if err != nil {
		return "", 0, err
	}
	build := answer.GetApprovedBuild()
	if build == nil || build.GetPrincipalId() != principalID {
		return "", 0, fmt.Errorf("%w: approved build", ErrUnreadableAuthorityAnswer)
	}
	switch build.GetState() {
	case v1.WorkContextApprovedBuildState_WORK_CONTEXT_APPROVED_BUILD_STATE_APPROVED:
		return build.GetImageDigest(), build.GetBuildIncarnation(), nil
	case v1.WorkContextApprovedBuildState_WORK_CONTEXT_APPROVED_BUILD_STATE_BEARS_NO_EXECUTION:
		// A person's session runs no approved build. This is the answer, not a
		// failure — and it is NOT what an unknown principal gets, which the host
		// refuses outright.
		return "", 0, corework.ErrNoApprovedBuild
	default:
		return "", 0, fmt.Errorf("%w: approved-build state", ErrUnreadableAuthorityAnswer)
	}
}

// OperationBinding resolves one binding by its opaque id, exactly. There is no
// method that searches bindings for one whose scopes would admit a call: scope
// containment is not binding identity, and a revoked narrow capability must not
// validate through a wider binding.
func (s *WorkContextSources) OperationBinding(
	ctx context.Context, bindingID string,
) (corework.OperationBinding, error) {
	if bindingID == "" {
		return corework.OperationBinding{}, fmt.Errorf("%w: operation binding", ErrInvalidAuthorityQuestion)
	}
	answer, err := s.ask(ctx, "binding\x00"+bindingID,
		func(request *v1.ModuleResolveWorkContextAuthorityRequest) {
			request.Question = &v1.ModuleResolveWorkContextAuthorityRequest_OperationBinding{
				OperationBinding: &v1.WorkContextOperationBindingQuestion{BindingId: bindingID},
			}
		})
	if err != nil {
		return corework.OperationBinding{}, err
	}
	binding := answer.GetOperationBinding()
	if binding == nil || binding.GetBindingId() != bindingID {
		return corework.OperationBinding{}, fmt.Errorf("%w: operation binding", ErrUnreadableAuthorityAnswer)
	}
	switch binding.GetState() {
	case v1.WorkContextOperationBindingState_WORK_CONTEXT_OPERATION_BINDING_STATE_RESOLVED:
		return corework.OperationBinding{
			ID:             binding.GetBindingId(),
			PrincipalID:    binding.GetPrincipalId(),
			InstallationID: binding.GetInstallationId(),
			Revision:       binding.GetRevision(),
			Incarnation:    binding.GetIncarnation(),
			Revoked:        binding.GetRevoked(),
		}, nil
	case v1.WorkContextOperationBindingState_WORK_CONTEXT_OPERATION_BINDING_STATE_ABSENT:
		return corework.OperationBinding{}, corework.ErrNoBinding
	default:
		return corework.OperationBinding{}, fmt.Errorf("%w: binding state", ErrUnreadableAuthorityAnswer)
	}
}

// AuthorizationRevision answers the revision a capability owned by this source's
// owner carries, for core's RevisionSource.
//
// Core passes only the tenant, and this source does not compare it: the host
// answers for the tenant the presented capability names and refuses one in any
// other organization, so there is no second tenant for an answer to be about.
// The owner is the capability's own.
func (s *WorkContextSources) AuthorizationRevision(ctx context.Context, _ string) (uint64, error) {
	answer, err := s.ask(ctx, "revision\x00"+s.owner,
		func(request *v1.ModuleResolveWorkContextAuthorityRequest) {
			request.Question = &v1.ModuleResolveWorkContextAuthorityRequest_AuthorizationRevision{
				AuthorizationRevision: &v1.WorkContextAuthorizationRevisionQuestion{
					OwnerPrincipalId: s.owner,
				},
			}
		})
	if err != nil {
		return 0, err
	}
	revision := answer.GetAuthorizationRevision()
	if revision == nil || revision.GetOwnerPrincipalId() != s.owner {
		return 0, fmt.Errorf("%w: authorization revision", ErrUnreadableAuthorityAnswer)
	}
	return revision.GetRevision(), nil
}

// Grant resolves the approval a capability's grant hop claims.
//
// The host answers NOT_RECORDED today, because it records no approval grants,
// and this returns an error for it: core's GrantSource has no absence sentinel,
// so "the issuer never made that decision" is a refusal. A state this client
// does not understand is also a refusal, which is what makes the day the host
// starts recording grants fail closed here rather than open.
func (s *WorkContextSources) Grant(ctx context.Context, grantID string) (*corework.Grant, error) {
	if grantID == "" {
		return nil, fmt.Errorf("%w: approval grant", ErrInvalidAuthorityQuestion)
	}
	answer, err := s.ask(ctx, "grant\x00"+grantID,
		func(request *v1.ModuleResolveWorkContextAuthorityRequest) {
			request.Question = &v1.ModuleResolveWorkContextAuthorityRequest_ApprovalGrant{
				ApprovalGrant: &v1.WorkContextApprovalGrantQuestion{GrantId: grantID},
			}
		})
	if err != nil {
		return nil, err
	}
	grant := answer.GetApprovalGrant()
	if grant == nil || grant.GetGrantId() != grantID {
		return nil, fmt.Errorf("%w: approval grant", ErrUnreadableAuthorityAnswer)
	}
	if grant.GetState() == v1.WorkContextApprovalGrantState_WORK_CONTEXT_APPROVAL_GRANT_STATE_NOT_RECORDED {
		return nil, fmt.Errorf("%w: %q", ErrNoApprovalGrant, grantID)
	}
	return nil, fmt.Errorf("%w: approval-grant state", ErrUnreadableAuthorityAnswer)
}
