package workcontext_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/codefly-dev/saas-sdk-go/workcontext"
)

func TestVerifyAcceptsAContextMintedForThisService(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})

	wc, err := v.Verify(context.Background(), mintValid(t))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if wc.GetIssuer() != workcontext.DefaultIssuer || wc.GetAudience() != testAudience {
		t.Errorf("iss/aud = %q/%q", wc.GetIssuer(), wc.GetAudience())
	}
	if wc.GetTyp() != "codefly.work-context/v1" {
		t.Errorf("typ = %q, want codefly.work-context/v1", wc.GetTyp())
	}
	if wc.GetTenantId() != "org-1" || wc.GetOwnerPrincipalId() != "user-1" {
		t.Errorf("tenant/owner = %q/%q", wc.GetTenantId(), wc.GetOwnerPrincipalId())
	}
	if wc.GetTaskId() != "task-1" || wc.GetSessionId() != "session-1" {
		t.Errorf("task/session = %q/%q", wc.GetTaskId(), wc.GetSessionId())
	}
	if wc.GetAuthorizationRevision() != 7 {
		t.Errorf("authorization_revision = %d, want 7", wc.GetAuthorizationRevision())
	}
	if actors := wc.GetActorChain(); len(actors) != 1 || actors[0].GetPrincipalId() != "agent-robin" {
		t.Errorf("actor_chain = %v", actors)
	}
}

// Verify does not enforce replay, so the policy and nonce a callee needs to
// enforce single-use itself must survive onto the verified claims. This guards
// the enforcement path the package doc points callees at against a regression
// that silently drops those fields.
func TestVerifyExposesReplayPolicyAndNonceForCalleeEnforcement(t *testing.T) {
	input := taskInput(testAudience)
	input.ReplayPolicy = "single-use"
	token, _, err := newSigner(t, signerOptions{}).StartTask(input)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	wc, err := newVerifier(t, workcontext.Config{Keys: staticKeys()}).Verify(context.Background(), token.Encoded())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if wc.GetReplayPolicy() != "single-use" {
		t.Errorf("replay_policy = %q, want single-use", wc.GetReplayPolicy())
	}
	if wc.GetNonce() == "" {
		t.Error("nonce is empty; a callee cannot enforce single-use without it")
	}
}

func TestVerifyRejectsEverythingThatIsNotOurs(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys()})
	cases := []struct {
		name   string
		token  string
		want   error
		status int
	}{
		// A sound token for another service is the one 403 among the rejections.
		{"wrong audience", mint(t, newSigner(t, signerOptions{}), "someone-else"), workcontext.ErrAudience, http.StatusForbidden},
		{"wrong issuer", mint(t, newSigner(t, signerOptions{issuer: "not-accounts"}), testAudience), workcontext.ErrInvalid, http.StatusUnauthorized},
		{"unknown kid", mint(t, newSigner(t, signerOptions{kid: "rotated-away"}), testAudience), workcontext.ErrInvalid, http.StatusUnauthorized},
		{"wrong key under the published kid", mint(t, newSigner(t, signerOptions{seed: 2}), testAudience), workcontext.ErrInvalid, http.StatusUnauthorized},
		// TTL is 5m and skew 1m: issued 7m ago expired 2m ago, past the skew.
		{"expired", mint(t, newSigner(t, signerOptions{now: testTime.Add(-7 * time.Minute)}), testAudience), workcontext.ErrInvalid, http.StatusUnauthorized},
		{"not yet valid", mint(t, newSigner(t, signerOptions{now: testTime.Add(2 * time.Minute)}), testAudience), workcontext.ErrInvalid, http.StatusUnauthorized},
		{"audience rewritten under the old signature", tampered(t), workcontext.ErrInvalid, http.StatusUnauthorized},
		{"garbage", "not.a-token", workcontext.ErrInvalid, http.StatusUnauthorized},
		{"three segments", "a.b.c", workcontext.ErrInvalid, http.StatusUnauthorized},
		{"empty", "", workcontext.ErrInvalid, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wc, err := v.Verify(context.Background(), tc.token)
			if wc != nil {
				t.Fatalf("Verify returned claims %v alongside err %v", wc, err)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := workcontext.HTTPStatus(err); got != tc.status {
				t.Errorf("HTTPStatus = %d, want %d", got, tc.status)
			}
		})
	}
}

func TestVerifyHonoursConfiguredIssuer(t *testing.T) {
	v := newVerifier(t, workcontext.Config{Keys: staticKeys(), Issuer: "accounts-staging"})

	if _, err := v.Verify(context.Background(), mint(t, newSigner(t, signerOptions{issuer: "accounts-staging"}), testAudience)); err != nil {
		t.Errorf("configured issuer rejected: %v", err)
	}
	if _, err := v.Verify(context.Background(), mintValid(t)); !errors.Is(err, workcontext.ErrInvalid) {
		t.Errorf("default issuer accepted under a configured one: %v", err)
	}
}

func TestNewVerifierRejectsUnsafeConfiguration(t *testing.T) {
	public, _ := testKey(1)
	cases := map[string]workcontext.Config{
		"no audience":          {Keys: staticKeys()},
		"blank audience":       {Audience: "  ", Keys: staticKeys()},
		"no key source":        {Audience: testAudience},
		"empty static key set": {Audience: testAudience, Keys: workcontext.StaticKeys(map[string]ed25519.PublicKey{})},
		"nil static key set":   {Audience: testAudience, Keys: workcontext.StaticKeys(nil)},
		"malformed public key": {Audience: testAudience, Keys: workcontext.StaticKeys(map[string]ed25519.PublicKey{testKID: public[:5]})},
		"relative JWKS URL":    {Audience: testAudience, Keys: workcontext.JWKS(workcontext.JWKSPath, nil)},
		"empty base URL":       {Audience: testAudience, Keys: workcontext.JWKS(workcontext.JWKSURL(""), nil)},
		"JWKS URL with query":  {Audience: testAudience, Keys: workcontext.JWKS("https://accounts.example.test/keys?tenant=x", nil)},
		"clock skew too wide":  {Audience: testAudience, Keys: staticKeys(), ClockSkew: time.Hour},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if v, err := workcontext.NewVerifier(cfg); err == nil {
				t.Fatalf("NewVerifier accepted %+v: %v", cfg, v)
			}
		})
	}
}

func TestJWKSURLJoinsTheAccountsPath(t *testing.T) {
	cases := map[string]string{
		"http://accounts:8080":            "http://accounts:8080/v1/auth/.well-known/jwks.json",
		"http://accounts:8080/":           "http://accounts:8080/v1/auth/.well-known/jwks.json",
		" https://gw.example.test/saas/ ": "https://gw.example.test/saas/v1/auth/.well-known/jwks.json",
	}
	for base, want := range cases {
		if got := workcontext.JWKSURL(base); got != want {
			t.Errorf("JWKSURL(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestJWKSFromGatewayCachesAndRefreshesOnAnUnknownKid(t *testing.T) {
	first, _ := testKey(1)
	second, _ := testKey(2)
	accounts := newJWKSServer(t, map[string]ed25519.PublicKey{testKID: first})
	v := newVerifier(t, workcontext.Config{Keys: workcontext.JWKSFromGateway(accounts.gateway())})

	// The first Verify fetches; the next ones hit the cache.
	for range 3 {
		if _, err := v.Verify(context.Background(), mintValid(t)); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
	if got := accounts.requests.Load(); got != 1 {
		t.Fatalf("JWKS fetched %d times, want 1", got)
	}

	// accounts rotates: a token under the new kid triggers exactly one
	// refresh, after which the retired key no longer verifies — and probing
	// with made-up kids does not fetch again within the same cache generation.
	accounts.rotate(map[string]ed25519.PublicKey{"accounts-2026-10": second})
	rotated := mint(t, newSigner(t, signerOptions{kid: "accounts-2026-10", seed: 2}), testAudience)
	if _, err := v.Verify(context.Background(), rotated); err != nil {
		t.Fatalf("Verify after rotation: %v", err)
	}
	if got := accounts.requests.Load(); got != 2 {
		t.Fatalf("JWKS fetched %d times after rotation, want 2", got)
	}
	if _, err := v.Verify(context.Background(), mintValid(t)); !errors.Is(err, workcontext.ErrInvalid) {
		t.Errorf("retired key still verifies: %v", err)
	}
	for _, kid := range []string{"guess-1", "guess-2", "guess-3"} {
		probe := mint(t, newSigner(t, signerOptions{kid: kid}), testAudience)
		if _, err := v.Verify(context.Background(), probe); !errors.Is(err, workcontext.ErrInvalid) {
			t.Errorf("unknown kid %q verified: %v", kid, err)
		}
	}
	if got := accounts.requests.Load(); got != 2 {
		t.Errorf("unknown kids caused %d fetches, want none beyond 2", got-2)
	}
}

func TestJWKSVerifierNeverFailsOpen(t *testing.T) {
	first, _ := testKey(1)
	cases := []struct {
		name  string
		setup func(t *testing.T) workcontext.KeySource
	}{
		{"empty key set", func(t *testing.T) workcontext.KeySource {
			accounts := newJWKSServer(t, nil)
			accounts.respond(http.StatusOK, `{"keys":[]}`)
			return workcontext.JWKSFromGateway(accounts.gateway())
		}},
		{"server error", func(t *testing.T) workcontext.KeySource {
			accounts := newJWKSServer(t, map[string]ed25519.PublicKey{testKID: first})
			accounts.respond(http.StatusServiceUnavailable, `{}`)
			return workcontext.JWKSFromGateway(accounts.gateway())
		}},
		{"not a JWKS", func(t *testing.T) workcontext.KeySource {
			accounts := newJWKSServer(t, nil)
			accounts.respond(http.StatusOK, `{"keys":"nope"}`)
			return workcontext.JWKSFromGateway(accounts.gateway())
		}},
		{"gateway without the JWKS route", func(t *testing.T) workcontext.KeySource {
			accounts := newJWKSServer(t, map[string]ed25519.PublicKey{testKID: first})
			return workcontext.JWKSFromGateway(gw{base: accounts.URL + "/not-accounts", client: accounts.Client()})
		}},
		{"unreachable", func(t *testing.T) workcontext.KeySource {
			accounts := newJWKSServer(t, map[string]ed25519.PublicKey{testKID: first})
			accounts.Close()
			return workcontext.JWKS(workcontext.JWKSURL(accounts.URL), nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newVerifier(t, workcontext.Config{Keys: tc.setup(t), RequestTimeout: time.Second})
			wc, err := v.Verify(context.Background(), mintValid(t))
			if wc != nil || !errors.Is(err, workcontext.ErrInvalid) {
				t.Fatalf("Verify = %v, %v; want ErrInvalid and no claims", wc, err)
			}
			if got := workcontext.HTTPStatus(err); got != http.StatusUnauthorized {
				t.Errorf("HTTPStatus = %d, want 401", got)
			}
		})
	}
}

func TestHTTPStatusMapsEverySentinel(t *testing.T) {
	cases := map[int][]error{
		http.StatusOK:                  {nil},
		http.StatusUnauthorized:        {workcontext.ErrMissing, workcontext.ErrInvalid, wrap(workcontext.ErrInvalid)},
		http.StatusForbidden:           {workcontext.ErrAudience, workcontext.ErrDenied, wrap(workcontext.ErrDenied)},
		http.StatusInternalServerError: {errors.New("something else")},
	}
	for want, errs := range cases {
		for _, err := range errs {
			if got := workcontext.HTTPStatus(err); got != want {
				t.Errorf("HTTPStatus(%v) = %d, want %d", err, got, want)
			}
		}
	}
}
