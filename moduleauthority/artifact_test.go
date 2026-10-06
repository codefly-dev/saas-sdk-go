package moduleauthority

import (
	"bytes"
	"connectrpc.com/connect"
	"errors"
	"math"
	"strings"
	"testing"
)

func artifactFixture(t *testing.T) ArtifactRequest {
	return ArtifactRequest{Parent: token(t, "parent.token"), InstallationID: "11111111-1111-4111-8111-111111111111", PolicyID: "content", Identity: ArtifactIdentity{Schema: "example.artifact/v1", Source: "acme.example", Subject: []byte(`{"files":[],"revision":9223372036854775807}`), ExpectedRevision: math.MaxInt64, Contracts: []ArtifactContract{{Kind: "engine", Name: "example/exact", Digest: "sha256:" + strings.Repeat("a", 64)}}}}
}
func TestArtifactCallsPreserveIdentityThroughRealGRPC(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	req := artifactFixture(t)
	for _, invoke := range []func() (ArtifactAuthorization, error){func() (ArtifactAuthorization, error) { return client.ApproveExecutableArtifact(t.Context(), req) }, func() (ArtifactAuthorization, error) { return client.AuthorizeExecutableArtifact(t.Context(), req) }} {
		out, err := invoke()
		if err != nil {
			t.Fatal(err)
		}
		if out.ExpectedRevision != math.MaxInt64 {
			t.Fatal("revision lost precision")
		}
	}
	calls := h.authority.snapshot()
	if len(calls) != 2 {
		t.Fatal("wrong authority call count")
	}
	for _, call := range calls {
		if call.artifact == nil || !bytes.Equal(call.artifact.Identity.Subject, req.Identity.Subject) || call.artifact.Identity.ExpectedRevision != math.MaxInt64 || call.artifact.ParentWorkContextToken != "parent.token" {
			t.Fatal("artifact identity changed in transport")
		}
		if len(call.workContext) != 1 || call.workContext[0] != "module.token1" || len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken {
			t.Fatal("module credentials not presented independently")
		}
	}
	if calls[0].method != "/saas.accounts.v1.ModuleCapabilitiesService/ApproveExecutableArtifact" || calls[1].method != "/saas.accounts.v1.ModuleCapabilitiesService/AuthorizeExecutableArtifact" {
		t.Fatal("approval and run calls conflated")
	}
	_, err := client.RevokeExecutableArtifact(t.Context(), ArtifactRevocation{Parent: req.Parent, InstallationID: req.InstallationID, ApprovalID: "11111111-1111-4111-8111-111111111111"})
	if err != nil {
		t.Fatal(err)
	}
	h.assertNoStrayPaths(t)
}
func TestArtifactDenialNeverApprovesAndReceiptsAreExact(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	req := artifactFixture(t)
	req.PolicyID = "denied"
	_, err := client.AuthorizeExecutableArtifact(t.Context(), req)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected denial, got %v", err)
	}
	calls := h.authority.snapshot()
	if len(calls) != 1 || calls[0].method != "/saas.accounts.v1.ModuleCapabilitiesService/AuthorizeExecutableArtifact" {
		t.Fatal("denial triggered fallback")
	}
	req.PolicyID = "mismatched-receipt"
	_, err = client.AuthorizeExecutableArtifact(t.Context(), req)
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("mismatched revision accepted: %v", err)
	}
	req.PolicyID = "content"
	req.Identity.Subject = make([]byte, 65537)
	_, err = client.ApproveExecutableArtifact(t.Context(), req)
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatal("oversize subject accepted")
	}
	if len(h.authority.snapshot()) != 2 {
		t.Fatal("invalid subject reached host")
	}
}
func TestArtifactRefreshRetainsExactApprovalRequest(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	_, err := client.ModuleWorkContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h.gateway.revoke("module.token1")
	req := artifactFixture(t)
	_, err = client.ApproveExecutableArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	calls := h.authority.snapshot()
	if len(calls) != 2 || !bytes.Equal(calls[0].artifact.Identity.Subject, calls[1].artifact.Identity.Subject) || calls[0].artifact.Identity.ExpectedRevision != calls[1].artifact.Identity.ExpectedRevision {
		t.Fatal("module credential refresh changed approval identity")
	}
}
