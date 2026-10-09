package moduleauthority

import "testing"

func TestAuditDeclarationUsesOwnModuleIdentity(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	types := []*ModuleAuditEventTypeDeclaration{{Type: "example.record.changed", Description: "Record changed", Visibility: ModuleAuditEventVisibility_MODULE_AUDIT_EVENT_VISIBILITY_TENANT, Fields: []*ModuleAuditFieldDeclaration{{Name: "record_id", Kind: ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_UUID}}}}
	if err := client.DeclareAuditEventTypes(t.Context(), types); err != nil {
		t.Fatal(err)
	}
	calls := h.authority.snapshot()
	if len(calls) != 1 {
		t.Fatalf("calls=%d", len(calls))
	}
	call := calls[0]
	if call.declaration.GetPrefix() != moduleCredentials.Prefix || call.declaration.GetTypes()[0].GetType() != types[0].Type {
		t.Fatal("declaration owner or type changed")
	}
	if len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken || len(call.workContext) != 1 || call.workContext[0] != "module.token1" || len(call.authorization) != 0 {
		t.Fatal("wrong declaration authority")
	}
	h.assertNoStrayPaths(t)
}
