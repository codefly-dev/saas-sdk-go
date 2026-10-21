package moduleauthority

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	corework "github.com/codefly-dev/core/workcontext"
)

var sealedTokens sync.Map

// sealedToken is a Work Context in core's encoding, minted with core's
// conformance fixture key (whose private half is public, so nothing trusts
// it) and named by its task ID. The same name is the same token for the whole
// test binary, so a fake host can recognise, issue and revoke one by name.
// The client only ever asks core whether a token is sealed; it never verifies
// one, so a fixture-signed token is exactly as good as a host-signed one here.
func sealedToken(name string) string {
	if token, ok := sealedTokens.Load(name); ok {
		return token.(string)
	}
	ctx := context.Background()
	_, key := corework.FixtureKeyPair()
	seals := corework.FixtureSeals()
	authority := &corework.Authority{
		Issuer: corework.FixtureIssuer, KeyID: corework.FixtureKeyID, Key: key,
		Revisions: corework.FixtureRevisions(), Seals: seals,
	}
	token, _, err := authority.Start(ctx, corework.StartInput{
		TenantID:           corework.FixtureTenant,
		OwnerPrincipalID:   corework.FixturePrincipal,
		OwnerPrincipalKind: "service",
		OrganizationID:     corework.FixtureOrganization,
		TaskID:             name,
		Audience:           corework.FixtureAudience,
		InstallationID:     corework.FixtureInstallation,
		TTL:                time.Hour,
	})
	if err != nil {
		panic(err)
	}
	stored, _ := sealedTokens.LoadOrStore(name, token)
	return stored.(string)
}

// TestSealedRefusesWhatCoreDoesNotDecode: the pre-cutover JSON payload — what
// a host before module-saas-starter#1093 mints — and anything else core does
// not decode is not a capability this client will carry.
func TestSealedRefusesWhatCoreDoesNotDecode(t *testing.T) {
	if !sealed(sealedToken("module.token1")) {
		t.Fatal("a core-format token was refused")
	}
	foreign := corework.FixtureForeignToken()
	if _, err := corework.Decode(foreign); !errors.Is(err, corework.ErrNotACoreToken) {
		t.Fatalf("core decodes its own foreign fixture: %v", err)
	}
	for name, encoded := range map[string]string{
		"empty":          "",
		"json payload":   foreign,
		"shape only":     "module.token1",
		"no signature":   "module",
		"not a protobuf": "bm90IGEgcHJvdG9idWY.c2ln",
	} {
		if sealed(encoded) {
			t.Errorf("%s: %q was accepted as a sealed capability", name, encoded)
		}
	}
}

// TestAMintAnsweringAForeignTokenIsRefused: a broker or authority endpoint
// that answers a token core does not decode yields ErrInvalidCapability, and
// the module capability is not cached.
func TestAMintAnsweringAForeignTokenIsRefused(t *testing.T) {
	h := newHost(t)
	h.gateway.foreign = true
	client := h.client(t, moduleCredentials)
	if _, err := client.ModuleWorkContext(t.Context()); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("module Work Context: %v, want ErrInvalidCapability", err)
	}
	if _, err := client.MintModuleOperationContext(t.Context(), "model"); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("operation context: %v, want ErrInvalidCapability", err)
	}
	if _, err := client.MintSourceOperationContext(t.Context(), exampleSource); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("source operation context: %v, want ErrInvalidCapability", err)
	}
	h.gateway.mu.Lock()
	h.gateway.foreign = false
	h.gateway.mu.Unlock()
	if module, err := client.ModuleWorkContext(t.Context()); err != nil || module != sealedToken("module.token2") {
		t.Fatalf("after the refusal: %v (a refused token must not be cached), err %v", module, err)
	}

	h = newHost(t)
	h.authority.foreign = true
	_, err := h.client(t, moduleCredentials).ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "binding-1", Parent: sealedToken("parent.token")})
	if !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("exchange: %v, want ErrInvalidCapability", err)
	}
}

// TestAnUnsealedParentIsRefusedBeforeAnyCall: a parent core does not decode
// is refused here, as invalid input, and nothing — not even the module's own
// Work Context mint — leaves the process.
func TestAnUnsealedParentIsRefusedBeforeAnyCall(t *testing.T) {
	foreign := corework.FixtureForeignToken()
	for name, call := range map[string]func(*Client) error{
		"exchange": func(c *Client) error {
			_, err := c.ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "binding-1", Parent: foreign})
			if !errors.Is(err, ErrInvalidExchange) {
				return err
			}
			return nil
		},
		"current installation": func(c *Client) error {
			_, err := c.GetCurrentInstallation(t.Context(), CurrentInstallationRequest{Parent: foreign, InstallationID: "11111111-1111-4111-8111-111111111111"})
			if !errors.Is(err, ErrInvalidInstallation) {
				return err
			}
			return nil
		},
		"artifact": func(c *Client) error {
			req := artifactFixture(t)
			req.Parent = foreign
			_, err := c.AuthorizeExecutableArtifact(t.Context(), req)
			if !errors.Is(err, ErrInvalidArtifact) {
				return err
			}
			return nil
		},
		"artifact revocation": func(c *Client) error {
			_, err := c.RevokeExecutableArtifact(t.Context(), ArtifactRevocation{Parent: foreign, InstallationID: "11111111-1111-4111-8111-111111111111", ApprovalID: "11111111-1111-4111-8111-111111111111"})
			if !errors.Is(err, ErrInvalidArtifact) {
				return err
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			if err := call(h.client(t, moduleCredentials)); err != nil {
				t.Fatalf("an unsealed parent was not refused as invalid input: %v", err)
			}
			if calls := h.authority.snapshot(); len(calls) != 0 {
				t.Fatalf("authority calls = %d, want none", len(calls))
			}
			if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 0 {
				t.Fatalf("broker calls = %v, want none", calls)
			}
		})
	}
}
