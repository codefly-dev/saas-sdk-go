package moduleauthority

import (
	"errors"
	"testing"
)

// A module that only mints builds its client without the authority seam: the
// broker's mints work, and every call that needs the authority endpoint fails
// with ErrNoAuthority before any request, instead of dialling an address the
// consumer had no reason to resolve.
func TestAMintOnlyClientMintsAndRefusesAuthorityCalls(t *testing.T) {
	h := newHost(t)
	seams := h.seams()
	seams.Authority = Authority{}
	client, err := New(seams, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if _, err := client.MintSourceOperationContext(t.Context(), exampleSource); err != nil {
		t.Fatalf("mint-only client could not mint: %v", err)
	}
	if _, err := client.ModuleWorkContext(t.Context()); err != nil {
		t.Fatalf("mint-only client could not mint its module Work Context: %v", err)
	}
	callsBefore, _, _, _ := h.gateway.snapshot()
	_, err = client.ExchangeOperation(t.Context(), ExchangeRequest{BindingID: "documents-ingest", Parent: token(t, "parent.token")})
	if !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("ExchangeOperation without an authority seam: %v, want ErrNoAuthority", err)
	}
	callsAfter, _, _, _ := h.gateway.snapshot()
	if len(callsAfter) != len(callsBefore) || len(h.authority.snapshot()) != 0 {
		t.Fatal("a refused authority call still reached the host")
	}
	h.assertNoStrayPaths(t)
}
