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

// nonMember is a recipient the fake host refuses as not a member of the tenant.
const nonMember = "99999999-9999-9999-9999-999999999999"

func TestNotifyUserCallsTheAuthorityEndpointAsTheModule(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)

	delivered, err := client.NotifyUser(t.Context(), UserNotice{
		Tenant: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222",
		Title: "Someone replied to your comment", Body: "Agreed.", Type: "info",
		ActionURL: "/documents/d1", Category: "product", IdempotencyKey: "a1@1:22222222-2222-2222-2222-222222222222",
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
	if call.method != "/saas.accounts.v1.ModuleCapabilitiesService/NotifyUser" {
		t.Fatalf("method = %q", call.method)
	}
	if len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken {
		t.Fatalf("x-codefly-internal-token = %v", call.internalToken)
	}
	if len(call.workContext) != 1 || call.workContext[0] != "module.token1" {
		t.Fatalf("x-codefly-work-context = %v", call.workContext)
	}
	n := call.userNotice
	if n.GetTenant() != "11111111-1111-1111-1111-111111111111" || n.GetUserId() != "22222222-2222-2222-2222-222222222222" ||
		n.GetTitle() != "Someone replied to your comment" || n.GetBody() != "Agreed." || n.GetType() != "info" ||
		n.GetActionUrl() != "/documents/d1" || n.GetCategory() != "product" ||
		n.GetIdempotencyKey() != "a1@1:22222222-2222-2222-2222-222222222222" {
		t.Fatalf("notice = %+v", n)
	}
	h.assertNoStrayPaths(t)
}

func TestNotifyUserReportsASuppressedNoticeAsUndelivered(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	delivered, err := client.NotifyUser(t.Context(), UserNotice{
		Tenant: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222",
		Title: "t", Category: "marketing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if delivered {
		t.Fatal("delivered = true for a notice the recipient's policy suppressed")
	}
}

func TestNotifyUserRefusesAnIncompleteNoticeBeforeAnyCall(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	const tenant, user = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	for _, notice := range []UserNotice{
		{UserID: user, Title: "t", Category: "product"},
		{Tenant: tenant, Title: "t", Category: "product"},
		{Tenant: tenant, UserID: user, Category: "product"},
		{Tenant: tenant, UserID: user, Title: "t"},
	} {
		if _, err := client.NotifyUser(t.Context(), notice); !errors.Is(err, ErrInvalidNotice) {
			t.Fatalf("%+v: err = %v, want ErrInvalidNotice", notice, err)
		}
	}
	if calls := h.authority.snapshot(); len(calls) != 0 {
		t.Fatalf("an incomplete notice reached the host %d times", len(calls))
	}
}

func TestNotifyUserSurfacesTheHostsRefusal(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	_, err := client.NotifyUser(t.Context(), UserNotice{
		Tenant: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222",
		Title: "t", Type: "sync", Category: "product",
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
	_, err = client.NotifyUser(t.Context(), UserNotice{
		Tenant: "11111111-1111-1111-1111-111111111111", UserID: nonMember, Title: "t", Category: "product",
	})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied for a recipient outside the tenant", err)
	}
}

func TestNotifyUserNeedsTheAuthoritySeam(t *testing.T) {
	h := newHost(t)
	seams := h.seams()
	seams.Authority = Authority{}
	client, err := New(seams, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.NotifyUser(t.Context(), UserNotice{
		Tenant: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222", Title: "t", Category: "product",
	}); !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("err = %v, want ErrNoAuthority", err)
	}
}
