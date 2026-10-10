package moduleauthority

import (
	"errors"
	"fmt"
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

// Recovery state belongs to the producer. Recreating an SDK client must not
// replace its persisted key, invent a new event, or replay an uncertain emit.
func TestAuditEmissionPreservesIntentAcrossClientRestart(t *testing.T) {
	h := newHost(t)
	event := AuditEvent{Tenant: testTenant, EventType: "example.changed", Actor: testOwner,
		Solution: "example", EntryID: "entry", Fields: map[string]any{"count": float64(3)}, IdempotencyKey: "unavailable"}
	for range 2 {
		client := h.client(t, moduleCredentials)
		if err := client.EmitAuditEvent(t.Context(), event); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("uncertain emit: %v", err)
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}
	calls := h.authority.snapshot()
	if len(calls) != 2 {
		t.Fatalf("got %d attempts for two explicit emits; uncertain effects must not be retried", len(calls))
	}
	for _, call := range calls {
		if call.method != "/saas.accounts.v1.ModuleCapabilitiesService/EmitAuditEvent" ||
			call.audit.GetTenant() != event.Tenant || call.audit.GetEventType() != event.EventType ||
			call.audit.GetActor() != event.Actor || call.audit.GetSolution() != event.Solution ||
			call.audit.GetEntryId() != event.EntryID || call.audit.GetIdempotencyKey() != event.IdempotencyKey ||
			!reflect.DeepEqual(call.audit.GetFields().AsMap(), event.Fields) {
			t.Fatal("restarting the client changed the caller's audit intent")
		}
	}
	h.assertNoStrayPaths(t)
}

func TestAuditEmissionRefreshesOnlyRejectedModuleAuthentication(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		rejectAll bool
		badToken  bool
		wantCode  connect.Code
		wantCalls int
	}{
		{name: "expired capability", wantCalls: 2},
		{name: "revoked module", rejectAll: true, wantCode: connect.CodeUnauthenticated, wantCalls: 2},
		{name: "invalid perimeter", badToken: true, wantCode: connect.CodePermissionDenied, wantCalls: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h := newHost(t)
			client := h.client(t, moduleCredentials)
			if _, err := client.ModuleWorkContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			h.gateway.revoke("module.token1")
			h.gateway.mu.Lock()
			h.gateway.rejectAll = scenario.rejectAll
			h.gateway.mu.Unlock()
			if scenario.badToken {
				client.authority.token = "invalid-perimeter-token"
			}
			event := AuditEvent{Tenant: testTenant, EventType: "example.changed", Actor: testOwner,
				Solution: "example", IdempotencyKey: "stable-key"}
			err := client.EmitAuditEvent(t.Context(), event)
			if scenario.wantCode == 0 && err != nil || scenario.wantCode != 0 && connect.CodeOf(err) != scenario.wantCode {
				t.Fatalf("emit error = %v, want code %v", err, scenario.wantCode)
			}
			calls := h.authority.snapshot()
			if len(calls) != scenario.wantCalls {
				t.Fatalf("got %d calls, want %d", len(calls), scenario.wantCalls)
			}
			for i, call := range calls {
				if len(call.workContext) != 1 || call.workContext[0] != fmt.Sprintf("module.token%d", i+1) || call.audit.GetIdempotencyKey() != event.IdempotencyKey {
					t.Fatal("authentication refresh changed the intent or reused the rejected credential")
				}
			}
			h.assertNoStrayPaths(t)
		})
	}
}

func TestAuditEmissionWithoutAuthorityDoesNotMint(t *testing.T) {
	h := newHost(t)
	seams := h.seams()
	seams.Authority = Authority{}
	client, err := New(seams, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	err = client.EmitAuditEvent(t.Context(), AuditEvent{EventType: "example.changed", Actor: testOwner, Solution: "example"})
	if !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("got %v, want ErrNoAuthority", err)
	}
	if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 0 || len(h.authority.snapshot()) != 0 {
		t.Fatal("missing authority seam still contacted the host")
	}
}
