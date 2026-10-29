package moduleauthority

import (
	"errors"
	"testing"
)

func TestOrganizationClientsNeverShareCapabilities(t *testing.T) {
	h := newHost(t)
	root := h.client(t, moduleCredentials)
	organizations := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	for i, organization := range organizations {
		child, err := root.ForOrganization(organization)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := child.ModuleWorkContext(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := child.ForOrganization(organizations[1-i]); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal("changed pinned organization")
		}
	}
	_, _, bodies, _ := h.gateway.snapshot()
	calls := bodies[hostWorkContextPath]
	if len(calls) != 2 {
		t.Fatalf("got %d mints, want one per organization", len(calls))
	}
	for i, org := range organizations {
		if calls[i]["organization_id"] != org {
			t.Fatalf("mint %d: %v", i, calls[i])
		}
	}
	if root.module != "" || root.credentials.OrganizationID != "" {
		t.Fatal("scoping mutated root")
	}
	for _, org := range []string{"", "not-an-organization", "11111111111141118111111111111111"} {
		if _, err := root.ForOrganization(org); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("accepted malformed selector %q", org)
		}
	}
}
