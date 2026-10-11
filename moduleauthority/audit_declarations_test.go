package moduleauthority

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	codefly "github.com/codefly-dev/sdk-go/workcontext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const auditDeclarationProcedure = "/saas.accounts.v1.ModuleCapabilitiesService/DeclareAuditEventTypes"

// Exercise real gRPC and binary-protobuf Connect. This bridge fixture models
// an explicitly selected host serving declarations, not every deployed host.
func forAuditTransports(t *testing.T, test func(*testing.T, host, *Client)) {
	t.Helper()
	for _, transport := range []string{"direct", "gateway"} {
		t.Run(transport, func(t *testing.T) {
			h := newHost(t)
			if transport == "direct" {
				test(t, h, h.client(t, moduleCredentials))
				return
			}
			procedure := "/modules/_authority" + auditDeclarationProcedure
			mux := http.NewServeMux()
			mux.Handle(procedure, connect.NewUnaryHandler(procedure, func(_ context.Context, req *connect.Request[v1.ModuleDeclareAuditEventTypesRequest]) (*connect.Response[v1.ModuleDeclareAuditEventTypesResponse], error) {
				call := authorityCall{
					method: auditDeclarationProcedure, declaration: req.Msg,
					internalToken: req.Header().Values("X-Codefly-Internal-Token"),
					workContext:   req.Header().Values(codefly.WorkContextHeaderName),
					authorization: req.Header().Values("Authorization"),
					moduleSecret:  req.Header().Values("X-Codefly-Module-Secret"),
				}
				h.authority.mu.Lock()
				h.authority.calls = append(h.authority.calls, call)
				h.authority.mu.Unlock()
				if req.Header().Get("Content-Type") != "application/proto" {
					return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("binary protobuf required"))
				}
				if len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken {
					return nil, connect.NewError(connect.CodePermissionDenied, errors.New("internal credential required"))
				}
				if len(call.workContext) != 1 || !h.gateway.accepts(call.workContext[0]) {
					return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("module capability rejected"))
				}
				if err := h.authority.declarationError; err != nil {
					verdict := status.Convert(err)
					refusal := connect.NewError(connect.Code(verdict.Code()), errors.New(verdict.Message()))
					for _, detail := range verdict.Proto().Details {
						wireDetail, err := connect.NewErrorDetail(detail)
						if err != nil {
							return nil, err
						}
						refusal.AddDetail(wireDetail)
					}
					return nil, refusal
				}
				return connect.NewResponse(&v1.ModuleDeclareAuditEventTypesResponse{}), nil
			}))
			mux.HandleFunc("/", h.gateway.serve)
			// Resolved gateway URLs can include a path and a trailing slash.
			server := httptest.NewServer(http.StripPrefix("/host", mux))
			t.Cleanup(server.Close)
			client, err := NewGatewayAuthority(Seams{
				Gateway:       staticGateway{url: server.URL + "/host/", client: server.Client()},
				InternalToken: testInternalToken,
			}, moduleCredentials)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			test(t, h, client)
		})
	}
}

func auditDeclarations() []*ModuleAuditEventTypeDeclaration {
	return []*ModuleAuditEventTypeDeclaration{
		{
			Type: "example.item.changed", Description: "An item changed",
			Visibility: ModuleAuditEventVisibility_MODULE_AUDIT_EVENT_VISIBILITY_TENANT,
			Fields: []*ModuleAuditFieldDeclaration{
				{Name: "item_id", Kind: ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_UUID},
				{Name: "state", Kind: ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_ENUM, Values: []string{"created", "updated"}},
				{Name: "contact", Kind: ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_STRING, Pii: true},
			},
		},
		{
			Type: "example.item.removed", Description: "An item was removed",
			Visibility: ModuleAuditEventVisibility_MODULE_AUDIT_EVENT_VISIBILITY_EXTERNAL,
			Fields:     []*ModuleAuditFieldDeclaration{{Name: "item_id", Kind: ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_UUID}},
		},
	}
}

func assertAuditCall(t *testing.T, call authorityCall, want *v1.ModuleDeclareAuditEventTypesRequest, moduleToken string) {
	t.Helper()
	if call.method != auditDeclarationProcedure || !proto.Equal(call.declaration, want) {
		t.Fatalf("declaration changed: method=%q, got=%v, want=%v", call.method, call.declaration, want)
	}
	if len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken || len(call.workContext) != 1 || call.workContext[0] != moduleToken || len(call.authorization) != 0 || len(call.moduleSecret) != 0 {
		t.Fatal("declaration must carry only the perimeter token and current module capability")
	}
}

func TestAuditDeclarationPreservesSchemaAndCachesModuleProof(t *testing.T) {
	forAuditTransports(t, func(t *testing.T, h host, client *Client) {
		input := &v1.ModuleDeclareAuditEventTypesRequest{Prefix: moduleCredentials.Prefix, Types: auditDeclarations()}
		want := proto.Clone(input).(*v1.ModuleDeclareAuditEventTypesRequest)
		for range 2 {
			if err := client.DeclareAuditEventTypes(t.Context(), input.Types); err != nil {
				t.Fatal(err)
			}
		}
		if !proto.Equal(input, want) {
			t.Fatal("declaration mutated the caller's schema")
		}
		calls := h.authority.snapshot()
		if len(calls) != 2 {
			t.Fatalf("authority calls=%d, want 2", len(calls))
		}
		for _, call := range calls {
			assertAuditCall(t, call, want, "module.token1")
		}
		if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 1 || calls[0] != hostWorkContextPath {
			t.Fatalf("broker calls=%v, want one cached module mint", calls)
		}
		h.assertNoStrayPaths(t)
	})
}

func TestAuditDeclarationRefreshPreservesSchema(t *testing.T) {
	forAuditTransports(t, func(t *testing.T, h host, client *Client) {
		if _, err := client.ModuleWorkContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		h.gateway.revoke("module.token1")
		input := &v1.ModuleDeclareAuditEventTypesRequest{Prefix: moduleCredentials.Prefix, Types: auditDeclarations()}
		want := proto.Clone(input).(*v1.ModuleDeclareAuditEventTypesRequest)
		if err := client.DeclareAuditEventTypes(t.Context(), input.Types); err != nil {
			t.Fatal(err)
		}
		calls := h.authority.snapshot()
		if len(calls) != 2 {
			t.Fatalf("authority calls=%d, want one rejection and one retry", len(calls))
		}
		assertAuditCall(t, calls[0], want, "module.token1")
		assertAuditCall(t, calls[1], want, "module.token2")
		if !proto.Equal(input, want) {
			t.Fatal("refresh mutated the caller's schema")
		}
		if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 2 {
			t.Fatalf("broker calls=%v, want initial mint and one refresh", calls)
		}
		h.assertNoStrayPaths(t)
	})
}

func TestAuditDeclarationPreservesHostRefusals(t *testing.T) {
	for _, code := range []codes.Code{codes.PermissionDenied, codes.InvalidArgument, codes.FailedPrecondition, codes.Unavailable, codes.Unauthenticated} {
		t.Run(code.String(), func(t *testing.T) {
			forAuditTransports(t, func(t *testing.T, h host, client *Client) {
				detail := &errdetails.ErrorInfo{Reason: "DECLARATION_REFUSED", Domain: "audit.example", Metadata: map[string]string{"type": "example.item.changed"}}
				verdict, err := status.New(code, "host declaration refusal").WithDetails(detail)
				if err != nil {
					t.Fatal(err)
				}
				h.authority.declarationError = verdict.Err()
				err = client.DeclareAuditEventTypes(t.Context(), auditDeclarations())
				var got *connect.Error
				if !errors.As(err, &got) || got.Code() != connect.Code(code) || got.Message() != verdict.Message() || len(got.Details()) != 1 {
					t.Fatalf("lost host refusal: %v", err)
				}
				gotDetail, err := got.Details()[0].Value()
				if err != nil || !proto.Equal(gotDetail, detail) {
					t.Fatalf("lost host error detail: %v, %v", gotDetail, err)
				}
				wantCalls := 1
				if code == codes.Unauthenticated {
					wantCalls = 2 // one refresh, then surface the repeated rejection
				}
				if calls := h.authority.snapshot(); len(calls) != wantCalls {
					t.Fatalf("authority calls=%d, want %d", len(calls), wantCalls)
				}
				if calls, _, _, _ := h.gateway.snapshot(); len(calls) != wantCalls {
					t.Fatalf("broker calls=%v, want %d", calls, wantCalls)
				}
				h.assertNoStrayPaths(t)
			})
		})
	}
}

func TestAuditDeclarationStopsAtBrokerRefusal(t *testing.T) {
	forAuditTransports(t, func(t *testing.T, h host, client *Client) {
		h.gateway.refuse[hostWorkContextPath] = refusal{http.StatusUnauthorized, "module identity refused"}
		err := client.DeclareAuditEventTypes(t.Context(), auditDeclarations())
		var brokerError *BrokerError
		if !errors.Is(err, ErrInvalidCredentials) || !errors.As(err, &brokerError) || brokerError.StatusCode != http.StatusUnauthorized {
			t.Fatalf("lost broker refusal: %v", err)
		}
		assertBrokerError(t, err, http.StatusUnauthorized)
		if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 1 || calls[0] != hostWorkContextPath {
			t.Fatalf("broker calls = %v, want only the work-context mint", calls)
		}
		if len(h.authority.snapshot()) != 0 {
			t.Fatal("declaration reached authority without a module capability")
		}
		h.assertNoStrayPaths(t)
	})
}

func TestAuditDeclarationNeedsAuthorityBeforeMinting(t *testing.T) {
	h := newHost(t)
	seams := h.seams()
	seams.Authority = Authority{}
	client, err := New(seams, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.DeclareAuditEventTypes(t.Context(), auditDeclarations()); !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("error=%v, want ErrNoAuthority", err)
	}
	if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 0 || len(h.authority.snapshot()) != 0 {
		t.Fatal("mint-only declaration must fail before any host request")
	}
	h.assertNoStrayPaths(t)
}

func TestAuditDeclarationDoesNotFallBackWhenBridgeIsAbsent(t *testing.T) {
	gateway := newFakeGateway(t) // broker routes only; authority bridge returns 404
	client, err := NewGatewayAuthority(Seams{Gateway: gateway, InternalToken: testInternalToken}, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.DeclareAuditEventTypes(t.Context(), auditDeclarations()); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("missing bridge error=%v, want unimplemented", err)
	}
	calls, stray, _, _ := gateway.snapshot()
	if len(calls) != 1 || calls[0] != hostWorkContextPath || len(stray) != 1 || stray[0] != "/modules/_authority"+auditDeclarationProcedure {
		t.Fatalf("unexpected retry or fallback: broker=%v, other=%v", calls, stray)
	}
}
