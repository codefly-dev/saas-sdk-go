// Package workcontext is the callee side of the Work Context contract: a
// composed solution service that receives a delegated `codefly.work-context/v1`
// capability — minted by accounts, forwarded by whoever acts on the user's
// behalf — verifies it here before trusting a single claim.
//
// sdk-go owns the wire format and the hard parts: shape, signature, time, issuer
// and type checks (codefly.WorkContextVerifier) and the rotation-aware,
// fail-closed JWKS cache (codefly.WorkContextJWKSVerifier). This package is the
// thin layer a callee actually wires up: it points those primitives at the
// accounts JWKS through the same Gateway seam the client facades use, pins the
// callee's own service name as the audience, stores the verified claims on the
// request context, and maps failures onto HTTP, Connect, and gRPC status codes.
//
//	verifier, err := workcontext.NewVerifier(workcontext.Config{
//		Audience: "lastlogin", // this service's name — the token's aud must match
//		Keys:     workcontext.JWKSFromGateway(gw),
//	})
//	mux.Handle("/api/", verifier.HTTPMiddleware(api))
//
//	// in a handler
//	if err := workcontext.RequireScope(r.Context(), "lastlogin:logins", "read"); err != nil {
//		http.Error(w, err.Error(), workcontext.HTTPStatus(err))
//		return
//	}
//
// Nothing here fails open: no keys, an unreachable JWKS, or an unknown key id
// all reject the request.
//
// Replay is not enforced here. Verification establishes authenticity and
// freshness — the signature and the signed time window — but a captured token
// is accepted on every call until it expires, regardless of its replay_policy.
// Enforcing a single-use policy needs a nonce store shared across the callee's
// replicas, which this stateless layer cannot own. A callee that must honour
// single-use reads the policy and nonce off the verified context and rejects a
// nonce it has already seen against its own store:
//
//	wc, _ := workcontext.FromContext(ctx)
//	if wc.GetReplayPolicy() == "single-use" && !nonces.Consume(wc.GetNonce()) {
//		return errReplayed
//	}
package workcontext

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codefly "github.com/codefly-dev/sdk-go"
)

const (
	// DefaultIssuer is the issuer accounts signs Work Contexts with.
	DefaultIssuer = "saas-starter"

	// JWKSPath is where accounts publishes its Ed25519 signing keys, relative
	// to the accounts (or gateway) base URL. The Work Context signing key is
	// the access-token key, under the same kid.
	JWKSPath = "/v1/auth/.well-known/jwks.json"
)

var (
	// ErrMissing reports a request that carries no Work Context at all.
	ErrMissing = errors.New("missing Codefly Work Context")

	// ErrInvalid is sdk-go's sentinel: every parse, signature, time, issuer,
	// type, and JWKS failure wraps it. A caller checks it with errors.Is.
	ErrInvalid = codefly.ErrWorkContextInvalid

	// ErrAudience reports a context that verifies but was minted for another
	// service. It is deliberately not an ErrInvalid: the token is sound, it is
	// simply not ours, which is a 403 rather than a 401.
	ErrAudience = errors.New("Codefly Work Context audience mismatch")

	// ErrDenied is sdk-go's sentinel for a scope the context does not grant.
	ErrDenied = codefly.ErrWorkContextDenied
)

// Gateway is the minimal surface this package needs from the solution runtime
// — the same shape the accounts and datasource facades ask for, so the
// runtime's gateway satisfies it as-is and the runtime takes no dependency on
// this package.
type Gateway interface {
	BaseURL() string
	HTTPClient() *http.Client
}

// KeySource says where a Verifier gets the Ed25519 public keys it trusts.
// Build one with JWKS, JWKSFromGateway, or StaticKeys.
type KeySource struct {
	kind       keySourceKind
	jwksURL    string
	httpClient *http.Client
	publicKeys map[string]ed25519.PublicKey
}

type keySourceKind int

const (
	keySourceNone keySourceKind = iota
	keySourceJWKS
	keySourceStatic
)

// JWKS fetches keys from an absolute JWKS URL. The client may be nil, in which
// case sdk-go's redirect-refusing default is used. Keys are cached (5 minutes
// by default) and re-fetched once per cache generation when a token names a
// key id the cache does not hold, so a rotation is picked up immediately
// without letting arbitrary key ids turn verification into a request loop.
func JWKS(url string, client *http.Client) KeySource {
	return KeySource{kind: keySourceJWKS, jwksURL: url, httpClient: client}
}

// JWKSFromGateway is JWKS pointed at the accounts endpoint behind a solution
// runtime gateway: JWKSURL(gw.BaseURL()) fetched with gw.HTTPClient().
func JWKSFromGateway(gw Gateway) KeySource {
	return JWKS(JWKSURL(gw.BaseURL()), gw.HTTPClient())
}

// JWKSURL joins an accounts (or gateway) base URL with JWKSPath.
func JWKSURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + JWKSPath
}

// StaticKeys pins a fixed kid → public-key set with no network access — for
// tests, or a deployment that distributes keys out of band. An empty set is
// rejected by NewVerifier rather than accepted as "trust nothing".
func StaticKeys(keys map[string]ed25519.PublicKey) KeySource {
	return KeySource{kind: keySourceStatic, publicKeys: keys}
}

// Config is what a callee has to say about itself and about accounts.
type Config struct {
	// Audience is this service's own name. A verified context whose aud is
	// anything else is rejected with ErrAudience. Required.
	Audience string
	// Issuer is the expected iss; empty means DefaultIssuer.
	Issuer string
	// Keys is the trusted key set. Required.
	Keys KeySource
	// Optional lets a request that carries no Work Context through the
	// middleware unauthenticated — FromContext then reports false. A context
	// that is present but does not verify is still rejected. Default: required.
	Optional bool

	// CacheTTL and RequestTimeout tune the JWKS cache (sdk-go defaults: 5m and
	// 2s); ignored for StaticKeys. Now and ClockSkew tune time validation
	// (defaults: time.Now and sdk-go's one-minute skew).
	CacheTTL       time.Duration
	RequestTimeout time.Duration
	Now            func() time.Time
	ClockSkew      time.Duration
}

// Verifier establishes trust in inbound Work Contexts for one service. It is
// safe for concurrent use; build one per process and share it across the
// HTTP, Connect, and gRPC entry points so they share the key cache.
type Verifier struct {
	audience string
	issuer   string
	optional bool

	// Exactly one of these is set, by the KeySource kind.
	jwks   *codefly.WorkContextJWKSVerifier
	static *codefly.WorkContextVerifier
}

// NewVerifier validates the configuration and does no network I/O; with a
// JWKS source the first Verify fetches the keys.
func NewVerifier(cfg Config) (*Verifier, error) {
	if strings.TrimSpace(cfg.Audience) == "" {
		return nil, errors.New("workcontext: Audience (this service's name) is required")
	}
	issuer := cfg.Issuer
	if issuer == "" {
		issuer = DefaultIssuer
	}
	v := &Verifier{audience: cfg.Audience, issuer: issuer, optional: cfg.Optional}
	switch cfg.Keys.kind {
	case keySourceJWKS:
		jwks, err := codefly.NewWorkContextJWKSVerifier(codefly.WorkContextJWKSVerifierOptions{
			URL:            cfg.Keys.jwksURL,
			HTTPClient:     cfg.Keys.httpClient,
			CacheTTL:       cfg.CacheTTL,
			RequestTimeout: cfg.RequestTimeout,
			Now:            cfg.Now,
			ClockSkew:      cfg.ClockSkew,
		})
		if err != nil {
			return nil, fmt.Errorf("workcontext: JWKS key source: %w", err)
		}
		v.jwks = jwks
	case keySourceStatic:
		static, err := codefly.NewWorkContextVerifier(codefly.WorkContextVerifierOptions{
			PublicKeys: cfg.Keys.publicKeys,
			Now:        cfg.Now,
			ClockSkew:  cfg.ClockSkew,
		})
		if err != nil {
			return nil, fmt.Errorf("workcontext: static key source: %w", err)
		}
		v.static = static
	default:
		return nil, errors.New("workcontext: Keys is required (JWKS, JWKSFromGateway, or StaticKeys)")
	}
	return v, nil
}

// Verify establishes trust in one encoded Work Context: shape, signature by
// kid, time window, issuer, type, and finally that the audience is this
// service. The returned claims are canonical and safe to authorize from.
//
// Failures wrap ErrInvalid, or ErrAudience for a sound token minted for
// another service; HTTPStatus / ConnectError / GRPCError map them onward.
//
// Verify does not enforce replay/single-use — see the package doc.
func (v *Verifier) Verify(ctx context.Context, encoded string) (*basev0.WorkContextV1, error) {
	token, err := codefly.ParseWorkContextToken(encoded)
	if err != nil {
		return nil, err
	}
	return v.verifyToken(ctx, token)
}

func (v *Verifier) verifyToken(ctx context.Context, token codefly.WorkContextToken) (*basev0.WorkContextV1, error) {
	// The audience is compared here rather than through the expectations so
	// a context minted for another service surfaces as ErrAudience (403)
	// instead of folding into ErrInvalid (401) with every other failure —
	// sdk-go reports all expectation mismatches under the one sentinel.
	expected := codefly.WorkContextExpectations{Issuer: v.issuer}
	var (
		claims *basev0.WorkContextV1
		err    error
	)
	if v.jwks != nil {
		claims, err = v.jwks.Verify(ctx, token, expected)
	} else {
		claims, err = v.static.Verify(token, expected)
	}
	if err != nil {
		return nil, err
	}
	if claims.GetAudience() != v.audience {
		return nil, fmt.Errorf("%w: minted for %q, this service is %q", ErrAudience, claims.GetAudience(), v.audience)
	}
	return claims, nil
}

// authenticate resolves the Work Context from the raw carrier values one
// transport read: the verified claims; (nil, nil) when the carrier is absent
// and the verifier is Optional; or the error to reject with. Presence and
// cardinality are decided here so every transport agrees on what "missing"
// (401, or pass-through) versus "present but wrong" (always rejected) means.
func (v *Verifier) authenticate(ctx context.Context, values []string) (*basev0.WorkContextV1, error) {
	switch len(values) {
	case 0:
		if v.optional {
			return nil, nil
		}
		return nil, ErrMissing
	case 1:
		token, err := codefly.ParseWorkContextToken(values[0])
		if err != nil {
			return nil, err
		}
		return v.verifyToken(ctx, token)
	default:
		return nil, fmt.Errorf("%w: exactly one Work Context carrier is allowed", ErrInvalid)
	}
}
