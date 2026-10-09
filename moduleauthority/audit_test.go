package moduleauthority

import (
	"errors"
	"reflect"
	"testing"

	"connectrpc.com/connect"
)

func TestAuditEmissionAndReceiptUseModuleAuthority(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	event := AuditEvent{Tenant: testTenant, EventType: "example.changed", Actor: testOwner,
		Solution: "example", EntryID: "entry", Fields: map[string]any{"count": float64(3)}, IdempotencyKey: "effect-1"}
	if err := client.EmitAuditEvent(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.LookupAuditEvent(t.Context(), event)
	if err != nil || receipt.EventID != "event-1" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	calls := h.authority.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls=%d", len(calls))
	}
	for i, method := range []string{"EmitAuditEvent", "LookupAuditEvent"} {
		call := calls[i]
		if call.method != "/saas.accounts.v1.ModuleCapabilitiesService/"+method {
			t.Fatal(call.method)
		}
		if len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken || len(call.workContext) != 1 || call.workContext[0] != "module.token1" || len(call.authorization) != 0 {
			t.Fatal("wrong authority carriers")
		}
		e := call.audit
		if e.GetTenant() != event.Tenant || e.GetActor() != event.Actor || e.GetEventType() != event.EventType || e.GetSolution() != event.Solution || e.GetEntryId() != event.EntryID || e.GetIdempotencyKey() != event.IdempotencyKey || !reflect.DeepEqual(e.GetFields().AsMap(), event.Fields) {
			t.Fatal("audit intent changed in transport")
		}
	}
	event.IdempotencyKey = "missing"
	receipt, err = client.LookupAuditEvent(t.Context(), event)
	if err != nil || receipt.EventID != "" {
		t.Fatal("missing receipt invented an outcome", receipt, err)
	}
	for _, attempt := range []struct {
		key  string
		code connect.Code
	}{{"conflict", connect.CodeFailedPrecondition}, {"unavailable", connect.CodeUnavailable}} {
		event.IdempotencyKey = attempt.key
		before := len(h.authority.snapshot())
		if err := client.EmitAuditEvent(t.Context(), event); connect.CodeOf(err) != attempt.code {
			t.Fatal(err)
		}
		if _, err := client.LookupAuditEvent(t.Context(), event); connect.CodeOf(err) != attempt.code {
			t.Fatal(err)
		}
		if len(h.authority.snapshot()) != before+2 {
			t.Fatal("effect error was retried")
		}
	}
	h.assertNoStrayPaths(t)
}

func TestAuditInvalidIntentDoesNotReachHost(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	for _, event := range []AuditEvent{
		{}, {EventType: "example.changed", Solution: "example"},
		{EventType: "example.changed", Actor: testOwner, Solution: "example", Fields: map[string]any{"bad": make(chan int)}},
	} {
		if err := client.EmitAuditEvent(t.Context(), event); !errors.Is(err, ErrInvalidAuditEvent) {
			t.Fatal(err)
		}
	}
	if _, err := client.LookupAuditEvent(t.Context(), AuditEvent{EventType: "example.changed", Actor: testOwner, Solution: "example"}); !errors.Is(err, ErrInvalidAuditEvent) {
		t.Fatal(err)
	}
	if len(h.authority.snapshot()) != 0 {
		t.Fatal("invalid intent reached host")
	}
}

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
