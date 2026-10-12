package moduleauthority

import (
	"connectrpc.com/connect"
	"errors"
	"testing"
)

func TestCurrentInstallationUsesIndependentCredentialsAndExactSelector(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	req := CurrentInstallationRequest{Parent: sealedToken("parent.token"), InstallationID: exampleSource}
	out, err := client.GetCurrentInstallation(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if out.InstallationID != exampleSource || out.TenantID != testTenant || out.TargetID != testOwner || out.BindingID != testBindingID {
		t.Fatal("wrong identity", out)
	}
	calls := h.authority.snapshot()
	if len(calls) != 1 {
		t.Fatal(calls)
	}
	call := calls[0]
	if call.installation == nil || call.installation.InstallationId != exampleSource || call.installation.ParentWorkContextToken != sealedToken("parent.token") {
		t.Fatal("selector or proof changed")
	}
	if len(call.authorization) != 0 || len(call.workContext) != 1 || call.workContext[0] != sealedToken("module.token1") || len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken {
		t.Fatal("independent authority not preserved")
	}
	if call.installation.ProtoReflect().Descriptor().Fields().Len() != 2 {
		t.Fatal("unexpected tenant/source selector")
	}
	h.assertNoStrayPaths(t)
}

func TestCurrentInstallationRefusesBadResponsesAndDoesNotFallback(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	for _, parent := range []string{sealedToken("wrong-id.token"), sealedToken("missing-tenant.token"), sealedToken("empty-target.token"), sealedToken("invalid-target.token"), sealedToken("empty-binding.token"), sealedToken("invalid-binding.token")} {
		_, err := client.GetCurrentInstallation(t.Context(), CurrentInstallationRequest{Parent: parent, InstallationID: exampleSource})
		if !errors.Is(err, ErrInvalidInstallation) {
			t.Fatalf("%s: %v", parent, err)
		}
	}
	before := len(h.authority.snapshot())
	_, err := client.GetCurrentInstallation(t.Context(), CurrentInstallationRequest{Parent: sealedToken("denied.token"), InstallationID: exampleSource})
	if connect.CodeOf(err) != connect.CodePermissionDenied || len(h.authority.snapshot()) != before+1 {
		t.Fatal("denial retried or lost", err)
	}
	_, err = client.GetCurrentInstallation(t.Context(), CurrentInstallationRequest{Parent: sealedToken("parent.token"), InstallationID: "bad"})
	if !errors.Is(err, ErrInvalidInstallation) || len(h.authority.snapshot()) != before+1 {
		t.Fatal("bad selector reached host")
	}
	h.assertNoStrayPaths(t)
}
