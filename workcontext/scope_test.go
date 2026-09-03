package workcontext_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"

	"github.com/codefly-dev/saas-sdk-go/workcontext"
)

func TestHasScopeAnswersFromTheLastActorsGrant(t *testing.T) {
	ctx := verifiedContext(t)
	cases := []struct {
		name   string
		kind   string
		action string
		ids    []string
		want   bool
	}{
		{"wildcard grant, kind-wide ask", "lastlogin:logins", "read", nil, true},
		{"wildcard grant covers any id", "lastlogin:logins", "read", []string{"anything"}, true},
		{"restricted grant, a granted id", "lastlogin:reports", "read", []string{"r-1"}, true},
		{"restricted grant, kind-wide ask", "lastlogin:reports", "read", nil, false},
		{"owner holds r-2 but the actor was attenuated to r-1", "lastlogin:reports", "read", []string{"r-2"}, false},
		{"owner may export, the actor may not", "lastlogin:reports", "export", []string{"r-1"}, false},
		{"every listed id must be granted", "lastlogin:reports", "read", []string{"r-1", "r-2"}, false},
		{"unknown kind", "lastlogin:secrets", "read", nil, false},
		{"unknown action", "lastlogin:logins", "delete", nil, false},
		{"namespace is part of the kind", "logins", "read", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workcontext.HasScope(ctx, tc.kind, tc.action, tc.ids...); got != tc.want {
				t.Errorf("HasScope(%s, %s, %v) = %v, want %v", tc.kind, tc.action, tc.ids, got, tc.want)
			}
		})
	}
}

func TestScopesFromContextFollowTheActorChain(t *testing.T) {
	// With an actor: the actor's attenuated grant, not the owner's authority.
	effective, ok := workcontext.ScopesFromContext(verifiedContext(t))
	if !ok {
		t.Fatal("verified context reported no scopes")
	}
	if len(effective) != 2 || effective[1].GetResourceKind() != "lastlogin:reports" {
		t.Fatalf("effective = %v", effective)
	}
	if ids := effective[1].GetResourceIds(); len(ids) != 1 || ids[0] != "r-1" {
		t.Errorf("actor's report ids = %v, want [r-1]", ids)
	}

	// Without one: the owner's authority applies as minted.
	direct := taskInput(testAudience)
	direct.ActorChain = nil
	token, _, err := newSigner(t, signerOptions{}).StartTask(direct)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	wc, err := newVerifier(t, workcontext.Config{Keys: staticKeys()}).Verify(context.Background(), token.Encoded())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	owner := workcontext.NewContext(context.Background(), wc)
	if !workcontext.HasScope(owner, "lastlogin:reports", "export", "r-2") {
		t.Error("owner's own authority should grant export on r-2")
	}
	if got, _ := workcontext.ScopesFromContext(owner); len(got) != len(wc.GetAuthorityScopes()) {
		t.Errorf("effective = %v, want the authority scopes", got)
	}
}

func TestScopesFromContextReturnsACopy(t *testing.T) {
	ctx := verifiedContext(t)
	first, _ := workcontext.ScopesFromContext(ctx)
	if len(first) == 0 {
		t.Fatal("no scopes to mutate")
	}

	// Reorder and overwrite the returned slice; the verified claims must be
	// untouched for the next read.
	first[0] = &basev0.WorkScopeV1{ResourceKind: "tampered"}
	first = append(first, &basev0.WorkScopeV1{ResourceKind: "injected"})
	_ = first

	second, _ := workcontext.ScopesFromContext(ctx)
	if second[0].GetResourceKind() == "tampered" || len(second) != 2 {
		t.Errorf("mutating the returned slice corrupted the verified claims: %v", second)
	}
}

func TestScopesFromContextIsFalseWithoutAVerifiedContext(t *testing.T) {
	if scopes, ok := workcontext.ScopesFromContext(context.Background()); ok || scopes != nil {
		t.Errorf("bare context reported scopes: %v, %v", scopes, ok)
	}
}

func TestRequireScopeSaysWhyAndCannotBeWidenedAfterVerify(t *testing.T) {
	ctx := verifiedContext(t)

	err := workcontext.RequireScope(ctx, "lastlogin:reports", "export", "r-1")
	if !errors.Is(err, workcontext.ErrDenied) {
		t.Fatalf("err = %v, want ErrDenied", err)
	}
	if !strings.Contains(err.Error(), "lastlogin:reports:export:r-1") {
		t.Errorf("denial does not name the capability: %v", err)
	}
	if got := workcontext.HTTPStatus(err); got != http.StatusForbidden {
		t.Errorf("HTTPStatus = %d, want 403", got)
	}

	// A handler that grows the verified proto by hand gets ErrInvalid, not a
	// grant: sdk-go re-checks attenuation against the owner's authority.
	wc, _ := workcontext.FromContext(ctx)
	actor := wc.GetActorChain()[0]
	actor.GrantedScopes = append(actor.GrantedScopes, &basev0.WorkScopeV1{ResourceKind: "lastlogin:secrets", Actions: []string{"read"}})
	if err := workcontext.RequireScope(ctx, "lastlogin:secrets", "read"); !errors.Is(err, workcontext.ErrInvalid) {
		t.Errorf("widened claims authorized: %v", err)
	}
	if workcontext.HasScope(ctx, "lastlogin:logins", "read") {
		t.Error("a structurally invalid context should grant nothing, even what it used to")
	}
}

func TestRequireScopeReportsMissingWithoutAContext(t *testing.T) {
	if err := workcontext.RequireScope(context.Background(), "lastlogin:logins", "read"); !errors.Is(err, workcontext.ErrMissing) {
		t.Errorf("no context: %v, want ErrMissing", err)
	}

	ctx := verifiedContext(t)
	if err := workcontext.RequireScope(ctx, "lastlogin:logins", "read"); err != nil {
		t.Errorf("granted scope: %v", err)
	}
	if err := workcontext.RequireScope(ctx, "lastlogin:reports", "read"); !errors.Is(err, workcontext.ErrDenied) {
		t.Errorf("restricted grant, kind-wide ask: %v, want ErrDenied", err)
	}
	if err := workcontext.RequireScope(ctx, "lastlogin:reports", "read", "r-1"); err != nil {
		t.Errorf("granted id: %v", err)
	}
}

func TestFromContextIsFalseForNilAndAbsent(t *testing.T) {
	if _, ok := workcontext.FromContext(context.Background()); ok {
		t.Error("absent context reported present")
	}
	if _, ok := workcontext.FromContext(workcontext.NewContext(context.Background(), nil)); ok {
		t.Error("nil context reported present")
	}
	if wc, ok := workcontext.FromContext(verifiedContext(t)); !ok || wc.GetTenantId() != "org-1" {
		t.Errorf("stored context = %v, %v", wc, ok)
	}
}
