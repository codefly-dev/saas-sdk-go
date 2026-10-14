package moduleauthority

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	corework "github.com/codefly-dev/core/workcontext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
)

// The live-authority sources, against the fake host.
//
// What these hold is the half of the contract this module owns: that each
// question carries the capability and the identifiers the host will check, and
// that every answer is TRANSLATED and never interpreted. The host's own half —
// which capability a module may ask about, and what the live answers are — is
// held in module-saas-starter, by a test that drives sdk-go/workcontext's
// PinnedVerifier against the real handler.
//
// Every state the wire can carry is exercised, including the UNSPECIFIED a host
// that said nothing would send and an answer on the wrong arm, because those
// are the cases where a client that guessed would turn the strongest check in
// the model into the easiest to defeat.

func authoritySources(t *testing.T, h host, state authorityState) (*WorkContextSources, *Client) {
	t.Helper()
	client := h.client(t, moduleCredentials)
	h.authority.authority = state
	sources, err := client.WorkContextSources(WorkContextSourceSettings{
		Capability: sealedToken("verified.capability"),
		// No cache, so each test counts the calls its own question makes.
		CacheEntries: -1,
	})
	if err != nil {
		t.Fatalf("WorkContextSources: %v", err)
	}
	return sources, client
}

// Every question carries the capability, and the identifiers it asks about are
// the ones the host will hold against that capability. The owner is read off
// the capability rather than taken from the consumer: one fewer value to get
// wrong, and the host refuses a misreading anyway.
func TestEveryAuthorityQuestionCarriesTheCapability(t *testing.T) {
	h := newHost(t)
	sources, _ := authoritySources(t, h, authorityState{
		principalState: v1.WorkContextPrincipalState_WORK_CONTEXT_PRINCIPAL_STATE_KNOWN,
		epoch:          7,
		revision:       12,
	})

	epoch, err := sources.PrincipalEpoch(context.Background(), corework.FixturePrincipal)
	if err != nil || epoch != 7 {
		t.Fatalf("PrincipalEpoch = %d, %v; want 7, nil", epoch, err)
	}
	revision, err := sources.AuthorizationRevision(context.Background(), corework.FixtureTenant)
	if err != nil || revision != 12 {
		t.Fatalf("AuthorizationRevision = %d, %v; want 12, nil", revision, err)
	}

	calls := h.authority.snapshot()
	questions := 0
	for _, call := range calls {
		question := call.authorityQuestion
		if question == nil {
			continue
		}
		questions++
		if question.GetWorkContext() != sealedToken("verified.capability") {
			t.Error("a question travelled without the capability it is about")
		}
		if revision := question.GetAuthorizationRevision(); revision != nil &&
			revision.GetOwnerPrincipalId() != corework.FixturePrincipal {
			t.Errorf("the revision question names owner %q, not the capability's", revision.GetOwnerPrincipalId())
		}
	}
	if questions != 2 {
		t.Fatalf("made %d questions, want 2", questions)
	}
}

// Each negative state is core's own sentinel, because a verifier turns each into
// a REFUSAL of the capability rather than into an outage.
func TestEveryAbsenceStateBecomesCoresSentinel(t *testing.T) {
	for _, probe := range []struct {
		what  string
		state authorityState
		call  func(*WorkContextSources) error
		want  error
	}{
		{
			what:  "an installation the principal does not hold",
			state: authorityState{sealState: v1.WorkContextSealState_WORK_CONTEXT_SEAL_STATE_UNHELD},
			call: func(s *WorkContextSources) error {
				_, err := s.Seal(context.Background(), corework.FixturePrincipal, corework.FixtureInstallation)
				return err
			},
			want: corework.ErrNoSeal,
		},
		{
			what:  "a principal the issuer does not hold",
			state: authorityState{principalState: v1.WorkContextPrincipalState_WORK_CONTEXT_PRINCIPAL_STATE_UNKNOWN},
			call: func(s *WorkContextSources) error {
				_, err := s.PrincipalEpoch(context.Background(), corework.FixturePrincipal)
				return err
			},
			want: corework.ErrNoSeal,
		},
		{
			what:  "a principal that bears no execution",
			state: authorityState{buildState: v1.WorkContextApprovedBuildState_WORK_CONTEXT_APPROVED_BUILD_STATE_BEARS_NO_EXECUTION},
			call: func(s *WorkContextSources) error {
				_, _, err := s.ApprovedBuild(context.Background(), corework.FixturePrincipal)
				return err
			},
			want: corework.ErrNoApprovedBuild,
		},
		{
			what:  "a binding the issuer does not hold",
			state: authorityState{bindingState: v1.WorkContextOperationBindingState_WORK_CONTEXT_OPERATION_BINDING_STATE_ABSENT},
			call: func(s *WorkContextSources) error {
				_, err := s.OperationBinding(context.Background(), "source-delegation/x/y")
				return err
			},
			want: corework.ErrNoBinding,
		},
		{
			what:  "an approval the issuer never recorded",
			state: authorityState{grantState: v1.WorkContextApprovalGrantState_WORK_CONTEXT_APPROVAL_GRANT_STATE_NOT_RECORDED},
			call: func(s *WorkContextSources) error {
				_, err := s.Grant(context.Background(), "g-1")
				return err
			},
			want: ErrNoApprovalGrant,
		},
	} {
		t.Run(probe.what, func(t *testing.T) {
			sources, _ := authoritySources(t, newHost(t), probe.state)
			if err := probe.call(sources); !errors.Is(err, probe.want) {
				t.Fatalf("%s: got %v, want %v", probe.what, err, probe.want)
			}
		})
	}
}

// AN ANSWER THIS CLIENT CANNOT READ IS A REFUSAL, never a permissive default.
// A host that said nothing (UNSPECIFIED), answered a different question, or
// answered about something else is refused — and the refusal is this module's
// own sentinel, so a caller can tell it from a capability that is simply
// revoked.
func TestAnUnreadableAnswerIsRefused(t *testing.T) {
	for _, probe := range []struct {
		what  string
		state authorityState
		call  func(*WorkContextSources) error
	}{
		{
			what:  "no seal state",
			state: authorityState{},
			call: func(s *WorkContextSources) error {
				_, err := s.Seal(context.Background(), corework.FixturePrincipal, corework.FixtureInstallation)
				return err
			},
		},
		{
			what:  "no principal state",
			state: authorityState{},
			call: func(s *WorkContextSources) error {
				_, err := s.PrincipalEpoch(context.Background(), corework.FixturePrincipal)
				return err
			},
		},
		{
			what:  "no approved-build state",
			state: authorityState{},
			call: func(s *WorkContextSources) error {
				_, _, err := s.ApprovedBuild(context.Background(), corework.FixturePrincipal)
				return err
			},
		},
		{
			what:  "no binding state",
			state: authorityState{},
			call: func(s *WorkContextSources) error {
				_, err := s.OperationBinding(context.Background(), "b-1")
				return err
			},
		},
		{
			what:  "no grant state",
			state: authorityState{},
			call: func(s *WorkContextSources) error {
				_, err := s.Grant(context.Background(), "g-1")
				return err
			},
		},
		{
			what:  "an answer about another installation",
			state: authorityState{sealState: v1.WorkContextSealState_WORK_CONTEXT_SEAL_STATE_HELD, installationID: "another-installation"},
			call: func(s *WorkContextSources) error {
				_, err := s.Seal(context.Background(), corework.FixturePrincipal, corework.FixtureInstallation)
				return err
			},
		},
		{
			what:  "an answer about another principal",
			state: authorityState{principalState: v1.WorkContextPrincipalState_WORK_CONTEXT_PRINCIPAL_STATE_KNOWN, epoch: 3, principalID: "someone-else"},
			call: func(s *WorkContextSources) error {
				_, err := s.PrincipalEpoch(context.Background(), corework.FixturePrincipal)
				return err
			},
		},
		{
			what:  "an answer on another arm",
			state: authorityState{mismatchedArm: true},
			call: func(s *WorkContextSources) error {
				_, err := s.PrincipalEpoch(context.Background(), corework.FixturePrincipal)
				return err
			},
		},
		{
			what:  "an empty answer",
			state: authorityState{emptyAnswer: true},
			call: func(s *WorkContextSources) error {
				_, err := s.PrincipalEpoch(context.Background(), corework.FixturePrincipal)
				return err
			},
		},
	} {
		t.Run(probe.what, func(t *testing.T) {
			sources, _ := authoritySources(t, newHost(t), probe.state)
			if err := probe.call(sources); !errors.Is(err, ErrUnreadableAuthorityAnswer) {
				t.Fatalf("%s: got %v, want ErrUnreadableAuthorityAnswer", probe.what, err)
			}
		})
	}
}

// A host refusal travels as it is, and the CODE survives the trip. The client
// adds no interpretation: the difference between "the issuer decided"
// (FailedPrecondition — the capability is revoked, mint again) and "the lookup
// failed" (Unavailable — retry) is the host's to state and the caller's to act
// on, so it has to arrive readable.
//
// It arrives as a connect code, which is what a caller reads: this client calls
// over Connect, so `connect.CodeOf` is the accessor and grpc's `status.Code`
// answers Unknown for the same error.
func TestAHostRefusalTravelsWithItsCode(t *testing.T) {
	for _, refusal := range []struct {
		sent error
		want connect.Code
	}{
		{status.Error(codes.FailedPrecondition, "the authority it asks about has changed"), connect.CodeFailedPrecondition},
		{status.Error(codes.Unavailable, "the lookup failed"), connect.CodeUnavailable},
		{status.Error(codes.PermissionDenied, "caller denied"), connect.CodePermissionDenied},
	} {
		h := newHost(t)
		sources, _ := authoritySources(t, h, authorityState{})
		h.authority.authorityRefusal = refusal.sent

		_, err := sources.PrincipalEpoch(context.Background(), corework.FixturePrincipal)

		if connect.CodeOf(err) != refusal.want {
			t.Fatalf("got %v (%s), want %s", err, connect.CodeOf(err), refusal.want)
		}
	}
}

// A capability this module cannot even read structurally is refused BEFORE a
// call: core's reader decides that, and a question about an unreadable
// capability is one the host would refuse anyway.
func TestSourcesRefuseACapabilityThatIsNotOne(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)

	for _, capability := range []string{"", "not-a-work-context", corework.FixtureForeignToken()} {
		if _, err := client.WorkContextSources(WorkContextSourceSettings{Capability: capability}); !errors.Is(err, ErrInvalidAuthorityQuestion) {
			t.Fatalf("capability %q: got %v, want ErrInvalidAuthorityQuestion", capability, err)
		}
	}
	for _, call := range h.authority.snapshot() {
		if call.authorityQuestion != nil {
			t.Fatal("a question was sent for a capability that could not be read")
		}
	}
}

// A cache bound above the ceiling is REFUSED rather than clamped: a cached
// answer delays a revocation by at most its bound, so the bound is a posture
// the caller chooses and must be told about when it cannot have it.
func TestACacheBoundAboveTheCeilingIsRefused(t *testing.T) {
	client := newHost(t).client(t, moduleCredentials)

	_, err := client.WorkContextSources(WorkContextSourceSettings{
		Capability: sealedToken("verified.capability"),
		CacheBound: AuthorityCacheBound + time.Second,
	})

	if !errors.Is(err, ErrInvalidAuthorityQuestion) {
		t.Fatalf("got %v, want ErrInvalidAuthorityQuestion", err)
	}
}

// The cache serves one answer for at most its bound, and a question that is not
// the same question is not served from it.
func TestTheCacheServesOneAnswerForAtMostItsBound(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	now := time.Now()
	sources, err := client.WorkContextSources(WorkContextSourceSettings{
		Capability: sealedToken("verified.capability"),
		CacheBound: time.Second,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("WorkContextSources: %v", err)
	}
	h.authority.authority = authorityState{
		principalState: v1.WorkContextPrincipalState_WORK_CONTEXT_PRINCIPAL_STATE_KNOWN, epoch: 4,
	}

	for range 3 {
		if _, err := sources.PrincipalEpoch(context.Background(), corework.FixturePrincipal); err != nil {
			t.Fatalf("PrincipalEpoch: %v", err)
		}
	}
	if asked := authorityQuestions(h); asked != 1 {
		t.Fatalf("asked %d times inside the bound, want 1", asked)
	}

	// Another principal is another question, so it is asked rather than served
	// from the first one's answer. A cache keyed on anything less specific than
	// the whole question would answer one question with another's answer.
	if _, err := sources.PrincipalEpoch(context.Background(), "another-principal"); err != nil {
		t.Fatalf("PrincipalEpoch for another principal: %v", err)
	}
	if asked := authorityQuestions(h); asked != 2 {
		t.Fatalf("asked %d times, want 2: a different question is not the cached one", asked)
	}

	// Past the bound, the answer is read again — which is what makes the bound
	// the longest a revocation can be delayed.
	now = now.Add(2 * time.Second)
	if _, err := sources.PrincipalEpoch(context.Background(), corework.FixturePrincipal); err != nil {
		t.Fatalf("PrincipalEpoch after the bound: %v", err)
	}
	if asked := authorityQuestions(h); asked != 3 {
		t.Fatalf("asked %d times after the bound, want 3", asked)
	}
}

// A REFUSAL IS NEVER CACHED. Re-asking a question that refused costs one call
// and keeps a transient failure from becoming a sticky one.
func TestARefusalIsNotCached(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	sources, err := client.WorkContextSources(WorkContextSourceSettings{
		Capability: sealedToken("verified.capability"),
	})
	if err != nil {
		t.Fatalf("WorkContextSources: %v", err)
	}
	h.authority.authorityRefusal = status.Error(codes.Unavailable, "the lookup failed")

	for range 2 {
		if _, err := sources.PrincipalEpoch(context.Background(), corework.FixturePrincipal); err == nil {
			t.Fatal("the host refused; the source must too")
		}
	}

	if asked := authorityQuestions(h); asked != 2 {
		t.Fatalf("asked %d times, want 2: a refusal must not be cached", asked)
	}
}

func authorityQuestions(h host) int {
	asked := 0
	for _, call := range h.authority.snapshot() {
		if call.authorityQuestion != nil {
			asked++
		}
	}
	return asked
}
