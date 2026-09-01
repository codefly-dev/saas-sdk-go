package workcontext_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codefly "github.com/codefly-dev/sdk-go"

	"github.com/codefly-dev/saas-sdk-go/workcontext"
)

// The tests mint real tokens with sdk-go's signer — the same code accounts
// runs — against deterministic keys, so every rejection below is a rejection
// of a well-formed context, not of a hand-rolled fake.

const (
	testAudience = "lastlogin"
	testKID      = "accounts-2026-09"
)

var testTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func testNow() time.Time { return testTime }

// testKey derives a deterministic Ed25519 key pair from a seed byte, so a test
// can name two distinct keys without randomness.
func testKey(seed byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	raw := make([]byte, ed25519.SeedSize)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	private := ed25519.NewKeyFromSeed(raw)
	return private.Public().(ed25519.PublicKey), private
}

// signerOptions is what a test varies about the authority it stands in for.
// Zero values mean "the real accounts": DefaultIssuer, the published kid,
// key seed 1, signing at testTime.
type signerOptions struct {
	issuer string
	kid    string
	seed   byte
	now    time.Time
}

func newSigner(t *testing.T, opts signerOptions) *codefly.WorkContextSigner {
	t.Helper()
	if opts.issuer == "" {
		opts.issuer = workcontext.DefaultIssuer
	}
	if opts.kid == "" {
		opts.kid = testKID
	}
	if opts.seed == 0 {
		opts.seed = 1
	}
	if opts.now.IsZero() {
		opts.now = testTime
	}
	_, private := testKey(opts.seed)
	signer, err := codefly.NewWorkContextSigner(codefly.WorkContextSignerOptions{
		Issuer:     opts.issuer,
		KeyID:      opts.kid,
		PrivateKey: private,
		Now:        func() time.Time { return opts.now },
	})
	if err != nil {
		t.Fatalf("NewWorkContextSigner: %v", err)
	}
	return signer
}

// taskInput is the context accounts would mint for an agent acting on a
// user's behalf in one org: the owner's authority covers two kinds, and the
// agent actor is attenuated to a single report and no export.
func taskInput(audience string) codefly.StartTaskInput {
	return codefly.StartTaskInput{
		Audience:              audience,
		TenantID:              "org-1",
		OwnerPrincipalID:      "user-1",
		TaskID:                "task-1",
		SessionID:             "session-1",
		AuthorizationRevision: 7,
		AuthorityScopes: []*basev0.WorkScopeV1{
			{ResourceKind: "lastlogin:logins", Actions: []string{"read"}},
			{ResourceKind: "lastlogin:reports", Actions: []string{"read", "export"}, ResourceIds: []string{"r-1", "r-2"}},
		},
		ActorChain: []*basev0.WorkActorV1{{
			PrincipalId:   "agent-robin",
			PrincipalKind: "agent",
			DelegationId:  "delegation-1",
			GrantedScopes: []*basev0.WorkScopeV1{
				{ResourceKind: "lastlogin:logins", Actions: []string{"read"}},
				{ResourceKind: "lastlogin:reports", Actions: []string{"read"}, ResourceIds: []string{"r-1"}},
			},
		}},
		TTL: 5 * time.Minute,
	}
}

// mint signs a task context for audience and returns the wire token.
func mint(t *testing.T, signer *codefly.WorkContextSigner, audience string) string {
	t.Helper()
	token, _, err := signer.StartTask(taskInput(audience))
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	return token.Encoded()
}

// mintValid is the common case: the real authority, our audience.
func mintValid(t *testing.T) string {
	t.Helper()
	return mint(t, newSigner(t, signerOptions{}), testAudience)
}

// tampered takes a context minted for another service and rewrites the
// audience claim to ours, keeping the original signature — the forgery a
// callee that checked aud without the signature would accept.
func tampered(t *testing.T) string {
	t.Helper()
	encoded := mint(t, newSigner(t, signerOptions{}), "someone-else")
	payloadSegment, signature, found := strings.Cut(encoded, ".")
	if !found {
		t.Fatalf("token has no signature segment: %q", encoded)
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadSegment)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	forged := bytes.Replace(payload, []byte(`"audience":"someone-else"`), []byte(`"audience":"`+testAudience+`"`), 1)
	if bytes.Equal(forged, payload) {
		t.Fatalf("audience claim not found in payload: %s", payload)
	}
	return base64.RawURLEncoding.EncodeToString(forged) + "." + signature
}

// staticKeys is the published key set, pinned without a network.
func staticKeys() workcontext.KeySource {
	public, _ := testKey(1)
	return workcontext.StaticKeys(map[string]ed25519.PublicKey{testKID: public})
}

// newVerifier fills the parts of cfg every test shares (our audience, the
// frozen clock) and fails the test on a configuration error.
func newVerifier(t *testing.T, cfg workcontext.Config) *workcontext.Verifier {
	t.Helper()
	if cfg.Audience == "" {
		cfg.Audience = testAudience
	}
	if cfg.Now == nil {
		cfg.Now = testNow
	}
	v, err := workcontext.NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// verifiedClaims is what a handler holds after the middleware ran.
func verifiedClaims(t *testing.T) *basev0.WorkContextV1 {
	t.Helper()
	wc, err := newVerifier(t, workcontext.Config{Keys: staticKeys()}).Verify(context.Background(), mintValid(t))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return wc
}

// seen records what a handler found on its context.
type seen struct {
	mu     sync.Mutex
	called bool
	ok     bool
	wc     *basev0.WorkContextV1
}

func (s *seen) record(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called = true
	s.wc, s.ok = workcontext.FromContext(ctx)
}

func (s *seen) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called, s.ok, s.wc = false, false, nil
}

// expect asserts the three things every transport test cares about: whether
// the handler ran, whether it found a context, and which tenant it was for.
func (s *seen) expect(t *testing.T, called, ok bool) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.called != called {
		t.Errorf("handler called = %v, want %v", s.called, called)
	}
	if s.ok != ok {
		t.Errorf("FromContext ok = %v, want %v", s.ok, ok)
	}
	if ok && s.wc.GetTenantId() != "org-1" {
		t.Errorf("tenant_id = %q, want org-1", s.wc.GetTenantId())
	}
}

// gw satisfies workcontext.Gateway against an httptest server.
type gw struct {
	base   string
	client *http.Client
}

func (g gw) BaseURL() string          { return g.base }
func (g gw) HTTPClient() *http.Client { return g.client }

// jwksServer stands in for accounts: it serves the JWKS at JWKSPath, lets a
// test rotate the key set or force a bad response, and counts fetches so a
// test can see the cache and the unknown-kid refresh at work.
type jwksServer struct {
	*httptest.Server
	mu       sync.Mutex
	keys     map[string]ed25519.PublicKey
	status   int
	body     []byte // when set, served verbatim instead of keys
	requests atomic.Int32
}

func newJWKSServer(t *testing.T, keys map[string]ed25519.PublicKey) *jwksServer {
	t.Helper()
	s := &jwksServer{keys: keys, status: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		if r.URL.Path != workcontext.JWKSPath {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		body := s.body
		if body == nil {
			body = jwksJSON(t, s.keys)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) rotate(keys map[string]ed25519.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys, s.body = keys, nil
}

func (s *jwksServer) respond(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.body = status, []byte(body)
}

func (s *jwksServer) gateway() gw {
	return gw{base: s.URL, client: s.Client()}
}

// jwksJSON renders keys the way accounts publishes them: OKP / Ed25519 with
// the raw public key base64url-encoded in x.
func jwksJSON(t *testing.T, keys map[string]ed25519.PublicKey) []byte {
	t.Helper()
	type jwk struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		Kid string `json:"kid"`
		X   string `json:"x"`
	}
	document := struct {
		Keys []jwk `json:"keys"`
	}{Keys: []jwk{}}
	for kid, public := range keys {
		document.Keys = append(document.Keys, jwk{
			Kty: "OKP", Crv: "Ed25519", Alg: "EdDSA", Use: "sig",
			Kid: kid, X: base64.RawURLEncoding.EncodeToString(public),
		})
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	return payload
}
