package datasource_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/codefly-dev/saas-sdk-go/datasource"
	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

type operationsHandler struct {
	accountsv1connect.UnimplementedDatasourceServiceHandler
	invoke  func(context.Context, *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error)
	lookup  func(context.Context, *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error)
	declare func(context.Context, *connect.Request[v1.DeclareSourceOperationsRequest]) (*connect.Response[v1.DeclareSourceOperationsResponse], error)
	list    func(context.Context, *connect.Request[v1.ListSourceOperationsRequest]) (*connect.Response[v1.ListSourceOperationsResponse], error)
}

func (h *operationsHandler) InvokeSourceOperation(ctx context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
	return h.invoke(ctx, req)
}

func (h *operationsHandler) LookupInvokeSourceOperation(ctx context.Context, req *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
	return h.lookup(ctx, req)
}

func (h *operationsHandler) DeclareSourceOperations(ctx context.Context, req *connect.Request[v1.DeclareSourceOperationsRequest]) (*connect.Response[v1.DeclareSourceOperationsResponse], error) {
	return h.declare(ctx, req)
}

func (h *operationsHandler) ListSourceOperations(ctx context.Context, req *connect.Request[v1.ListSourceOperationsRequest]) (*connect.Response[v1.ListSourceOperationsResponse], error) {
	return h.list(ctx, req)
}

type personContextKey struct{}

type gatewayTransport struct {
	testing *testing.T
	inner   http.RoundTripper
}

func (g gatewayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// No SDK-owned identity or routing headers may reach the gateway. The
	// gateway, and only the gateway, forwards the original person's token.
	for key := range req.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-codefly-") && !strings.EqualFold(key, "x-codefly-effect-id") {
			g.testing.Errorf("SDK set gateway-owned header %q", key)
		}
	}
	cloned := req.Clone(req.Context())
	if token, ok := req.Context().Value(personContextKey{}).(string); ok {
		cloned.Header.Set("x-codefly-work-context", token)
	}
	return g.inner.RoundTrip(cloned)
}

func newOperationsClient(t *testing.T, h *operationsHandler, opts ...connect.ClientOption) *datasource.Client {
	t.Helper()
	return datasource.New(newOperationsGateway(t, h), opts...)
}

func newOperationsGateway(t *testing.T, h *operationsHandler) gw {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(accountsv1connect.NewDatasourceServiceHandler(h))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport = gatewayTransport{testing: t, inner: client.Transport}
	return gw{base: server.URL, client: client}
}

func committedReceipt(id string) *v1.SourceOperationReceipt {
	return &v1.SourceOperationReceipt{
		EffectId: id, Status: "committed",
		CommittedAt: time.Unix(1_791_540_000, 123).UTC().Format(time.RFC3339Nano), ProviderStatus: 200, OutputJson: `{}`,
	}
}

func TestInvokeForwardsGatewayContextMintsEffectAndNeverDeduplicates(t *testing.T) {
	const token = "opaque.original.person.work-context"
	var calls atomic.Int32
	var first *v1.InvokeSourceOperationRequest
	h := &operationsHandler{invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
		calls.Add(1)
		if req.Header().Get("x-codefly-work-context") != token {
			t.Error("gateway's Work Context was changed or lost")
		}
		for key := range req.Header() {
			if strings.HasPrefix(strings.ToLower(key), "x-codefly-") &&
				!strings.EqualFold(key, "x-codefly-work-context") && !strings.EqualFold(key, "x-codefly-effect-id") {
				t.Errorf("unexpected SDK header %q", key)
			}
		}
		if req.Msg.GetOrgId() != "org-1" || req.Msg.GetSourceId() != "source-1" || req.Msg.GetOperation() != "list_invoices" {
			t.Errorf("selectors = %v", req.Msg)
		}
		if req.Header().Get("x-codefly-effect-id") != req.Msg.GetEffectId() {
			t.Error("header and request effect IDs differ")
		}
		id, err := uuid.Parse(req.Msg.GetEffectId())
		if err != nil || id.Version() != 7 || id.Variant() != uuid.RFC4122 {
			t.Errorf("effect ID is not UUIDv7: %q", req.Msg.GetEffectId())
		}
		if first == nil {
			first = proto.Clone(req.Msg).(*v1.InvokeSourceOperationRequest)
		} else if !proto.Equal(first, req.Msg) {
			t.Error("same-effect replay changed input")
		}
		return connect.NewResponse(&v1.InvokeSourceOperationResponse{
			OutputJson: `{"count":2}`, Receipt: committedReceipt(req.Msg.GetEffectId()),
		}), nil
	}}
	c := newOperationsClient(t, h)
	ctx := context.WithValue(context.Background(), personContextKey{}, token)
	result, err := c.Invoke(ctx, "org-1", "source-1", "list_invoices", struct {
		Limit int `json:"limit"`
	}{Limit: 20}, datasource.WithDeadline(time.Now().Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.EffectID != first.GetEffectId() || result.Receipt.Status != datasource.ReceiptStatusCommitted ||
		!result.Receipt.CommittedAt.Equal(time.Unix(1_791_540_000, 123)) || result.Receipt.ProviderStatus != 200 {
		t.Fatalf("receipt = %+v", result.Receipt)
	}
	var output map[string]int
	if err := json.Unmarshal(result.Output, &output); err != nil || output["count"] != 2 {
		t.Fatalf("output = %s, error = %v", result.Output, err)
	}
	second, err := c.Invoke(ctx, "org-1", "source-1", "list_invoices", map[string]any{"limit": 20}, datasource.WithEffectID(result.Receipt.EffectID))
	if err != nil || !reflect.DeepEqual(result, second) {
		t.Fatalf("replay = %+v, %v", second, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("host got %d invokes, want 2 (no local deduplication)", calls.Load())
	}
}

func TestInvokeChangedInputReachesHostAndIsRefused(t *testing.T) {
	var calls atomic.Int32
	var first string
	h := &operationsHandler{invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
		calls.Add(1)
		if first != "" && first != req.Msg.GetInputJson() {
			return nil, connectError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{Reason: "SOURCE_EFFECT_REUSED"})
		}
		first = req.Msg.GetInputJson()
		return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: `{}`, Receipt: committedReceipt(req.Msg.GetEffectId())}), nil
	}}
	c := newOperationsClient(t, h)
	result, err := c.Invoke(context.Background(), "org", "source", "create_invoice", map[string]any{"amount": 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Invoke(context.Background(), "org", "source", "create_invoice", map[string]any{"amount": 2}, datasource.WithEffectID(result.Receipt.EffectID))
	if !errors.Is(err, datasource.ErrEffectReused) || calls.Load() != 2 {
		t.Fatalf("refusal = %v, host calls = %d", err, calls.Load())
	}
}

func connectError(t *testing.T, code connect.Code, details ...proto.Message) error {
	t.Helper()
	err := connect.NewError(code, errors.New("credential-looking-example-secret"))
	for _, message := range details {
		detail, detailErr := connect.NewErrorDetail(message)
		if detailErr != nil {
			t.Fatal(detailErr)
		}
		err.AddDetail(detail)
	}
	return err
}

func TestInvokeHostErrorsOverConnect(t *testing.T) {
	for _, tt := range []struct {
		name  string
		err   error
		check func(*testing.T, error, time.Time, time.Time)
	}{
		{"permission", connectError(t, connect.CodePermissionDenied), checkSentinel(datasource.ErrNotPermitted)},
		{"operation", connectError(t, connect.CodeNotFound), checkSentinel(datasource.ErrUnknownOperation)},
		{"unknown", connectError(t, connect.CodeUnavailable), checkSentinel(datasource.ErrOutcomeUnknown)},
		{"lost mutation", connectError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{Reason: "SOURCE_OPERATION_OUTCOME_UNKNOWN"}), checkSentinel(datasource.ErrOutcomeUnknown)},
		{"reused effect", connectError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{Reason: "SOURCE_EFFECT_REUSED"}), checkSentinel(datasource.ErrEffectReused)},
		{"input", connectError(t, connect.CodeInvalidArgument, &errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "/invoice/id"}}}),
			func(t *testing.T, err error, _, _ time.Time) {
				var value *datasource.InputError
				if !errors.As(err, &value) || value.Pointer != "/invoice/id" {
					t.Errorf("input error = %v", err)
				}
			}},
		{"rate limit", connectError(t, connect.CodeResourceExhausted, &errdetails.ErrorInfo{Reason: "DATASOURCE_RATE_LIMITED"}, &errdetails.RetryInfo{RetryDelay: durationpb.New(5 * time.Second)}),
			func(t *testing.T, err error, before, after time.Time) {
				var value *datasource.RateLimited
				if !errors.As(err, &value) || value.ResetAt.Before(before.Add(5*time.Second)) || value.ResetAt.After(after.Add(5*time.Second)) {
					t.Errorf("rate limit = %#v", value)
				}
			}},
		{"provider", connectError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{Reason: "SOURCE_PROVIDER_REFUSED"}),
			func(t *testing.T, err error, _, _ time.Time) {
				var value *datasource.ProviderRefused
				if !errors.As(err, &value) || value.Status != 0 {
					t.Errorf("provider refusal = %v", err)
				}
			}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newOperationsClient(t, &operationsHandler{invoke: func(context.Context, *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
				calls.Add(1)
				return nil, tt.err
			}})
			before := time.Now()
			result, err := c.Invoke(context.Background(), "org", "source", "create_invoice", map[string]any{})
			tt.check(t, err, before, time.Now())
			if result == nil || result.Receipt.EffectID == "" {
				t.Fatal("RPC error lost the minted effect ID")
			}
			if errors.Is(err, datasource.ErrOutcomeUnknown) {
				var refused *datasource.ProviderRefused
				var transport *connect.Error
				var outcome *datasource.OutcomeUnknown
				if errors.As(err, &refused) || errors.As(err, &transport) || result.Receipt.Status != datasource.ReceiptStatusUnknown {
					t.Fatalf("unknown outcome misclassified: %+v, %v", result, err)
				}
				if !errors.As(err, &outcome) || outcome.Receipt.EffectID != result.Receipt.EffectID ||
					connect.CodeOf(outcome.Cause()) != connect.CodeOf(tt.err) || outcome.Cause().Error() != tt.err.Error() {
					t.Fatal("explicit outcome diagnostics lost the Connect code, message, or effect ID")
				}
			}
			if err.Error() != tt.err.Error() {
				t.Errorf("host error message was changed: %q", err)
			}
			if calls.Load() != 1 {
				t.Errorf("SDK retried the effect: %d calls", calls.Load())
			}
		})
	}
}

func checkSentinel(want error) func(*testing.T, error, time.Time, time.Time) {
	return func(t *testing.T, err error, _, _ time.Time) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("error = %v, want %v", err, want)
		}
	}
}

func TestLookupUnknownIsReceiptAndNeverInvokes(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		c := newOperationsClient(t, &operationsHandler{lookup: func(_ context.Context, req *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
			if req.Msg.GetEffectId() != "effect" || req.Msg.ProtoReflect().Descriptor().Fields().Len() != 1 {
				t.Errorf("lookup selectors = %v", req.Msg)
			}
			if req.Header().Get("x-codefly-effect-id") != "" {
				t.Error("lookup introduced an effect header")
			}
			receipt := committedReceipt("effect")
			if unknown {
				receipt.Status = "unknown"
				receipt.CommittedAt = ""
				receipt.ProviderStatus = 0
			}
			return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: `{}`, Receipt: receipt}), nil
		}})
		receipt, err := c.Lookup(context.Background(), "org", "source", "effect")
		if unknown != errors.Is(err, datasource.ErrOutcomeUnknown) || receipt == nil || receipt.EffectID != "effect" {
			t.Fatalf("lookup = %+v, %v", receipt, err)
		}
		if unknown && (receipt.Status != datasource.ReceiptStatusUnknown || !receipt.CommittedAt.IsZero() || receipt.ProviderStatus != 0) {
			t.Fatalf("unknown receipt = %+v", receipt)
		}
	}
}

func TestLookupUnavailableRetainsUnknownReceipt(t *testing.T) {
	c := newOperationsClient(t, &operationsHandler{lookup: func(context.Context, *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("host unreachable"))
	}})
	receipt, err := c.Lookup(context.Background(), "org", "source", "effect")
	if receipt == nil || receipt.EffectID != "effect" || receipt.Status != datasource.ReceiptStatusUnknown || !errors.Is(err, datasource.ErrOutcomeUnknown) {
		t.Fatalf("unavailable lookup = %+v, %v", receipt, err)
	}
}

func TestOperationDeclarationsRoundTrip(t *testing.T) {
	var declared []*v1.SourceOperation
	const token = "original.person"
	check := func(orgID, sourceID string, header http.Header) {
		if orgID != "org" || sourceID != "source" || header.Get("x-codefly-work-context") != token {
			t.Error("selectors or gateway context were lost")
		}
	}
	c := newOperationsClient(t, &operationsHandler{
		declare: func(_ context.Context, req *connect.Request[v1.DeclareSourceOperationsRequest]) (*connect.Response[v1.DeclareSourceOperationsResponse], error) {
			check(req.Msg.GetOrgId(), req.Msg.GetSourceId(), req.Header())
			declared = req.Msg.GetOperations()
			for _, operation := range declared {
				if operation.GetDigest() != "" {
					t.Error("declaration sent a caller-supplied digest")
				}
				operation.Digest = "host-digest"
			}
			return connect.NewResponse(&v1.DeclareSourceOperationsResponse{}), nil
		},
		list: func(_ context.Context, req *connect.Request[v1.ListSourceOperationsRequest]) (*connect.Response[v1.ListSourceOperationsResponse], error) {
			check(req.Msg.GetOrgId(), req.Msg.GetSourceId(), req.Header())
			return connect.NewResponse(&v1.ListSourceOperationsResponse{Operations: declared}), nil
		},
	})
	ctx := context.WithValue(context.Background(), personContextKey{}, token)
	for _, effect := range []datasource.Effect{datasource.EffectReadOnly, datasource.EffectMutation} {
		want := datasource.Operation{
			Name: "list_invoices", Method: "GET", Path: "/accounts/{id}/invoices", Query: []string{"limit"},
			Description: "List available invoices", Digest: "caller-digest-is-ignored",
			Input: json.RawMessage(`{"type":"object","additionalProperties":false}`), Output: json.RawMessage(`{"type":"object"}`),
			Effect: effect, MaxOutputBytes: 65536,
		}
		if err := c.DeclareOperations(ctx, "org", "source", []datasource.Operation{want}); err != nil {
			t.Fatal(err)
		}
		want.Digest = "host-digest"
		got, err := c.ListOperations(ctx, "org", "source")
		if err != nil || len(got) != 1 {
			t.Fatalf("list = %+v, %v", got, err)
		}
		for _, pair := range [][2]json.RawMessage{{want.Input, got[0].Input}, {want.Output, got[0].Output}} {
			var left, right any
			if err := json.Unmarshal(pair[0], &left); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(pair[1], &right); err != nil || !reflect.DeepEqual(left, right) {
				t.Errorf("schema changed: %s -> %s", pair[0], pair[1])
			}
		}
		got[0].Input, got[0].Output = want.Input, want.Output
		if !reflect.DeepEqual(got[0], want) {
			t.Errorf("operation = %+v, want %+v", got[0], want)
		}
	}
	if err := c.DeclareOperations(ctx, "org", "source", nil); err != nil {
		t.Fatal(err)
	}
	got, err := c.ListOperations(ctx, "org", "source")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty replacement = %+v, %v", got, err)
	}
}

func TestInvokeDeadlineRetainsEffectID(t *testing.T) {
	var calls atomic.Int32
	c := newOperationsClient(t, &operationsHandler{invoke: func(ctx context.Context, _ *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
		calls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	result, err := c.Invoke(context.Background(), "org", "source", "create_invoice", map[string]any{},
		datasource.WithDeadline(time.Now().Add(100*time.Millisecond)))
	if !errors.Is(err, datasource.ErrOutcomeUnknown) || result == nil || result.Receipt.EffectID == "" || calls.Load() != 1 {
		t.Fatalf("deadline = %+v, %v, calls = %d", result, err, calls.Load())
	}
}

func TestUnknownAndMalformedReceiptsRetainOriginalEffectID(t *testing.T) {
	for _, tt := range []struct {
		name    string
		receipt *v1.SourceOperationReceipt
	}{
		{"unknown", &v1.SourceOperationReceipt{EffectId: "effect", Status: "unknown"}},
		{"missing", nil},
		{"mismatch", committedReceipt("another-effect")},
		{"unspecified", &v1.SourceOperationReceipt{EffectId: "effect"}},
		{"invalid timestamp", &v1.SourceOperationReceipt{EffectId: "effect", Status: "committed", CommittedAt: "invalid-time"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newOperationsClient(t, &operationsHandler{
				invoke: func(context.Context, *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
					calls.Add(1)
					return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: `{}`, Receipt: tt.receipt}), nil
				},
				lookup: func(context.Context, *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
					return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: `{}`, Receipt: tt.receipt}), nil
				},
			})
			result, err := c.Invoke(context.Background(), "org", "source", "create_invoice", map[string]any{}, datasource.WithEffectID("effect"))
			if !errors.Is(err, datasource.ErrOutcomeUnknown) || result == nil || result.Receipt.EffectID != "effect" || calls.Load() != 1 {
				t.Fatalf("result = %+v, %v, host calls = %d", result, err, calls.Load())
			}
			receipt, err := c.Lookup(context.Background(), "org", "source", "effect")
			var outcome *datasource.OutcomeUnknown
			if !errors.As(err, &outcome) || !errors.Is(err, datasource.ErrOutcomeUnknown) || receipt == nil || receipt.EffectID != "effect" ||
				outcome.Receipt.EffectID != "effect" || calls.Load() != 1 {
				t.Fatalf("lookup = %+v, %v, invoke calls = %d", receipt, err, calls.Load())
			}
		})
	}
}

func TestInvokeInvalidJSONDoesNotReachHost(t *testing.T) {
	var calls atomic.Int32
	c := newOperationsClient(t, &operationsHandler{invoke: func(context.Context, *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
		calls.Add(1)
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected request"))
	}})
	for _, input := range []any{nil, []int{1}, make(chan int)} {
		result, err := c.Invoke(context.Background(), "org", "source", "create_invoice", input)
		var invalid *datasource.InputError
		if result != nil || !errors.As(err, &invalid) {
			t.Fatalf("invalid input = %+v, %v", result, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid input reached host %d times", calls.Load())
	}
}

func TestInvokeAddsOnlyEffectHeaderToGeneratedClient(t *testing.T) {
	gateway := newOperationsGateway(t, &operationsHandler{invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
		return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: `{}`, Receipt: committedReceipt(req.Msg.GetEffectId())}), nil
	}})
	var headers []http.Header
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			headers = append(headers, req.Header().Clone())
			return next(ctx, req)
		}
	})
	option := connect.WithInterceptors(interceptor)
	raw := accountsv1connect.NewDatasourceServiceClient(gateway.HTTPClient(), gateway.BaseURL(), option)
	_, err := raw.InvokeSourceOperation(context.Background(), connect.NewRequest(&v1.InvokeSourceOperationRequest{
		OrgId: "org", SourceId: "source", Operation: "list_invoices", EffectId: "effect", InputJson: `{}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = datasource.New(gateway, option).Invoke(context.Background(), "org", "source", "list_invoices", map[string]any{}, datasource.WithEffectID("effect"))
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 2 || headers[1].Get("x-codefly-effect-id") != "effect" {
		t.Fatalf("recorded headers = %v", headers)
	}
	headers[1].Del("x-codefly-effect-id")
	if !reflect.DeepEqual(headers[0], headers[1]) {
		t.Fatalf("facade added headers beyond effect ID: generated %v, facade %v", headers[0], headers[1])
	}
}

func TestWithDeadlineKeepsEarlierParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	want, _ := ctx.Deadline()
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if got, ok := ctx.Deadline(); !ok || !got.Equal(want) {
				t.Errorf("deadline = %v, want parent %v", got, want)
			}
			return next(ctx, req)
		}
	})
	c := newOperationsClient(t, &operationsHandler{invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
		return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: `{}`, Receipt: committedReceipt(req.Msg.GetEffectId())}), nil
	}}, connect.WithInterceptors(interceptor))
	if _, err := c.Invoke(ctx, "org", "source", "list_invoices", map[string]any{}, datasource.WithDeadline(time.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
}

func TestInvokeJSONTextPreservesNumbersOverConnect(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts []connect.ClientOption
	}{
		{"binary", nil},
		{"json", []connect.ClientOption{connect.WithProtoJSON()}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const input = `{"decimal":1.234567890123456789,"id":9007199254740993}`
			const output = " {\"id\":18446744073709551615,\"value\":1.234567890123456789} "
			c := newOperationsClient(t, &operationsHandler{invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
				if req.Msg.GetInputJson() != input {
					t.Errorf("input JSON changed: %s", req.Msg.GetInputJson())
				}
				receipt := committedReceipt(req.Msg.GetEffectId())
				receipt.OutputJson = output
				return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: output, Receipt: receipt}), nil
			}}, tt.opts...)
			for _, inputValue := range []any{
				json.RawMessage(input),
				map[string]any{"id": int64(9007199254740993), "decimal": json.Number("1.234567890123456789")},
			} {
				result, err := c.Invoke(context.Background(), "org", "source", "list_invoices", inputValue)
				if err != nil || string(result.Output) != output {
					t.Fatalf("result = %+v, %v", result, err)
				}
			}
		})
	}
}

func TestInvokeInvalidHostOutputRetainsCommittedReceipt(t *testing.T) {
	for _, output := range []string{"", "null", "[]", "{} {}", `{"incomplete"`} {
		c := newOperationsClient(t, &operationsHandler{invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
			return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: output, Receipt: committedReceipt(req.Msg.GetEffectId())}), nil
		}})
		result, err := c.Invoke(context.Background(), "org", "source", "list_invoices", map[string]any{})
		if err == nil || result == nil || result.Receipt.Status != datasource.ReceiptStatusCommitted || result.Receipt.EffectID == "" {
			t.Fatalf("invalid output %q: %+v, %v", output, result, err)
		}
	}
}

func TestLookupHostOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name      string
		hostError error
		want      error
		status    datasource.ReceiptStatus
	}{
		{"not attempted", connect.NewError(connect.CodeNotFound, errors.New("source effect not found")), datasource.ErrEffectNotFound, datasource.ReceiptStatusNotAttempted},
		{"lost mutation", connectError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{Reason: "SOURCE_OPERATION_OUTCOME_UNKNOWN"}), datasource.ErrOutcomeUnknown, datasource.ReceiptStatusUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var lookups, invokes atomic.Int32
			c := newOperationsClient(t, &operationsHandler{
				lookup: func(context.Context, *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
					lookups.Add(1)
					return nil, tt.hostError
				},
				invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
					invokes.Add(1)
					if req.Msg.GetEffectId() != "persisted-effect" {
						t.Error("reinvoke changed the effect ID")
					}
					return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: `{}`, Receipt: committedReceipt(req.Msg.GetEffectId())}), nil
				},
			})
			receipt, err := c.Lookup(context.Background(), "org", "source", "persisted-effect")
			var refused *datasource.ProviderRefused
			if !errors.Is(err, tt.want) || errors.Is(err, datasource.ErrUnknownOperation) || errors.As(err, &refused) ||
				receipt == nil || receipt.EffectID != "persisted-effect" || receipt.Status != tt.status || receipt.Output != nil {
				t.Fatalf("lookup = %+v, %v", receipt, err)
			}
			if lookups.Load() != 1 || invokes.Load() != 0 || err.Error() != tt.hostError.Error() {
				t.Fatal("lookup retried, invoked, or changed the host message")
			}
			if errors.Is(err, datasource.ErrEffectNotFound) {
				_, err = c.Invoke(context.Background(), "org", "source", "create_invoice", json.RawMessage(`{}`), datasource.WithEffectID(receipt.EffectID))
				if err != nil || invokes.Load() != 1 {
					t.Fatalf("explicit same-ID invoke = %v, calls = %d", err, invokes.Load())
				}
			}
		})
	}
}

func TestLookupReturnsCommittedOutputWithoutInvoking(t *testing.T) {
	const output = `{"id":18446744073709551615,"amount":1.234567890123456789}`
	c := newOperationsClient(t, &operationsHandler{lookup: func(context.Context, *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
		receipt := committedReceipt("effect")
		receipt.OutputJson = output
		return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: output, Receipt: receipt}), nil
	}})
	receipt, err := c.Lookup(context.Background(), "org", "source", "effect")
	if err != nil || receipt.Status != datasource.ReceiptStatusCommitted || string(receipt.Output) != output {
		t.Fatalf("lookup = %+v, %v", receipt, err)
	}
}

func TestInvalidEffectIDNeverDispatches(t *testing.T) {
	var calls atomic.Int32
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			calls.Add(1)
			return next(ctx, req)
		}
	})
	c := newOperationsClient(t, &operationsHandler{}, connect.WithInterceptors(interceptor))
	for _, id := range []string{"", "bad\nid", "bad\rid", "bad\tid", " leading", "trailing ", "bad\x00id", "bad\x7fid", "\xff", strings.Repeat("x", 129), strings.Repeat("é", 65)} {
		var input *datasource.InputError
		result, err := c.Invoke(context.Background(), "org", "source", "create_invoice", json.RawMessage(`{}`), datasource.WithEffectID(id))
		if result != nil || !errors.As(err, &input) || errors.Is(err, datasource.ErrOutcomeUnknown) {
			t.Fatalf("invoke ID %q = %+v, %v", id, result, err)
		}
		receipt, err := c.Lookup(context.Background(), "org", "source", id)
		if receipt != nil || !errors.As(err, &input) {
			t.Fatalf("lookup ID %q = %+v, %v", id, receipt, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid IDs dispatched %d RPCs", calls.Load())
	}
}

func TestEffectIDBoundaryReachesHostUnchanged(t *testing.T) {
	for _, id := range []string{strings.Repeat("x", 126) + "!~", "effect with spaces", "facture-é", "发票", strings.Repeat("é", 64)} {
		c := newOperationsClient(t, &operationsHandler{invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
			if req.Msg.GetEffectId() != id || req.Header().Get("x-codefly-effect-id") != id {
				t.Error("effect ID changed")
			}
			return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: `{}`, Receipt: committedReceipt(id)}), nil
		}})
		if _, err := c.Invoke(context.Background(), "org", "source", "list_invoices", json.RawMessage(`{}`), datasource.WithEffectID(id)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCanceledBeforeDispatchIsNotUnknown(t *testing.T) {
	var calls atomic.Int32
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			calls.Add(1)
			return next(ctx, req)
		}
	})
	c := newOperationsClient(t, &operationsHandler{}, connect.WithInterceptors(interceptor))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tt := range []struct {
		ctx  context.Context
		opts []datasource.InvokeOption
		want error
	}{
		{canceled, nil, context.Canceled},
		{context.Background(), []datasource.InvokeOption{datasource.WithDeadline(time.Now().Add(-time.Second))}, context.DeadlineExceeded},
	} {
		result, err := c.Invoke(tt.ctx, "org", "source", "create_invoice", json.RawMessage(`{}`), tt.opts...)
		if !errors.Is(err, tt.want) || errors.Is(err, datasource.ErrOutcomeUnknown) || result == nil ||
			result.Receipt.EffectID == "" || result.Receipt.Status != datasource.ReceiptStatusNotAttempted {
			t.Fatalf("preflight = %+v, %v", result, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("canceled call dispatched %d RPCs", calls.Load())
	}
}
