package moduleauthority

import (
	"connectrpc.com/connect"
	"errors"
	codefly "github.com/codefly-dev/sdk-go/workcontext"
	"testing"
)

func TestRuntimeBoundaryIndependentModuleAuthority(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	out, err := client.VerifyWorkContextRuntimeBoundary(t.Context(), token(t, "viewer.token"))
	if err != nil {
		t.Fatal(err)
	}
	if out.TenantID != testTenant || out.BoundaryID != exampleSource {
		t.Fatal(out)
	}
	calls := h.authority.snapshot()
	if len(calls) != 1 {
		t.Fatal(calls)
	}
	call := calls[0]
	if call.boundary == nil || call.boundary.ForwardedWorkContextToken != "viewer.token" || call.boundary.ProtoReflect().Descriptor().Fields().Len() != 1 {
		t.Fatal("forwarded proof or contract changed")
	}
	if len(call.workContext) != 1 || call.workContext[0] != "module.token1" || len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken || len(call.authorization) != 0 {
		t.Fatal("module credentials not independent")
	}
	for _, parent := range []string{"missing-tenant.token", "missing-boundary.token"} {
		_, err = client.VerifyWorkContextRuntimeBoundary(t.Context(), token(t, parent))
		if !errors.Is(err, ErrInvalidRuntimeBoundary) {
			t.Fatal(err)
		}
	}
	before := len(h.authority.snapshot())
	_, err = client.VerifyWorkContextRuntimeBoundary(t.Context(), token(t, "denied.token"))
	if connect.CodeOf(err) != connect.CodePermissionDenied || len(h.authority.snapshot()) != before+1 {
		t.Fatal("denial changed or retried", err)
	}
	_, err = client.VerifyWorkContextRuntimeBoundary(t.Context(), codefly.WorkContextToken{})
	if !errors.Is(err, ErrInvalidRuntimeBoundary) || len(h.authority.snapshot()) != before+1 {
		t.Fatal("empty proof reached host", err)
	}
	h.assertNoStrayPaths(t)
}
