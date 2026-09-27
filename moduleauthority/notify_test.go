package moduleauthority

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
)

func TestNotifyOrgAdminsCallsTheAuthorityEndpointAsTheModule(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)

	delivered, err := client.NotifyOrgAdmins(t.Context(), OrgAdminsNotice{
		Tenant: "11111111-1111-1111-1111-111111111111", Title: "A delegation is invalid",
		Body: "Reconnect the source.", Type: "warning", Category: "security", IdempotencyKey: "delegation-invalid-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("delivered = false")
	}
	calls := h.authority.snapshot()
	if len(calls) != 1 {
		t.Fatalf("authority calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.method != "/saas.accounts.v1.ModuleCapabilitiesService/NotifyOrgAdmins" {
		t.Fatalf("method = %q", call.method)
	}
	if len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken {
		t.Fatalf("x-codefly-internal-token = %v", call.internalToken)
	}
	if len(call.workContext) != 1 || call.workContext[0] != "module.token1" {
		t.Fatalf("x-codefly-work-context = %v", call.workContext)
	}
	n := call.notice
	if n.GetTenant() != "11111111-1111-1111-1111-111111111111" || n.GetType() != "warning" ||
		n.GetCategory() != "security" || n.GetIdempotencyKey() != "delegation-invalid-1" || n.GetBody() != "Reconnect the source." {
		t.Fatalf("notice = %+v", n)
	}
	h.assertNoStrayPaths(t)
}

func TestNotifyOrgAdminsRefusesAnIncompleteNoticeBeforeAnyCall(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	for _, notice := range []OrgAdminsNotice{
		{Title: "t", Category: "security"},
		{Tenant: "11111111-1111-1111-1111-111111111111", Category: "security"},
		{Tenant: "11111111-1111-1111-1111-111111111111", Title: "t"},
	} {
		if _, err := client.NotifyOrgAdmins(t.Context(), notice); !errors.Is(err, ErrInvalidNotice) {
			t.Fatalf("%+v: err = %v, want ErrInvalidNotice", notice, err)
		}
	}
	if calls := h.authority.snapshot(); len(calls) != 0 {
		t.Fatalf("an incomplete notice reached the host %d times", len(calls))
	}
}

func TestNotifyOrgAdminsSurfacesTheHostsRefusal(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	_, err := client.NotifyOrgAdmins(t.Context(), OrgAdminsNotice{
		Tenant: "11111111-1111-1111-1111-111111111111", Title: "t", Type: "sync", Category: "security",
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestNotifyOrgAdminsNeedsTheAuthoritySeam(t *testing.T) {
	h := newHost(t)
	seams := h.seams()
	seams.Authority = Authority{}
	client, err := New(seams, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.NotifyOrgAdmins(t.Context(), OrgAdminsNotice{
		Tenant: "11111111-1111-1111-1111-111111111111", Title: "t", Category: "security",
	}); !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("err = %v, want ErrNoAuthority", err)
	}
}
