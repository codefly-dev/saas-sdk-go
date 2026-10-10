package datasource_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"

	"github.com/codefly-dev/saas-sdk-go/datasource"
	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
)

func parityCall(c *datasource.Client, method string) (*datasource.Receipt, error) {
	ctx := context.Background()
	switch method {
	case "invoke":
		result, err := c.Invoke(ctx, "org", "source", "read_item", map[string]any{})
		if result == nil {
			return nil, err
		}
		return &result.Receipt, err
	case "lookup":
		return c.Lookup(ctx, "org", "source", "persisted-effect")
	case "declare":
		return nil, c.DeclareOperations(ctx, "org", "source", []datasource.Operation{{
			Name: "read_item", Method: "GET", Path: "/items", Effect: datasource.EffectReadOnly,
			Input: json.RawMessage(`{}`), Output: json.RawMessage(`{}`),
		}})
	default:
		_, err := c.ListOperations(ctx, "org", "source")
		return nil, err
	}
}

func wireDetail(t *testing.T, message proto.Message) string {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]string{
		"type": string(message.ProtoReflect().Descriptor().FullName()), "value": base64.RawStdEncoding.EncodeToString(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestNilAndUnknownWireDetailsPreserveOutcome(t *testing.T) {
	unknownReason := wireDetail(t, &errdetails.ErrorInfo{Reason: "SOURCE_OPERATION_OUTCOME_UNKNOWN", Domain: "saas.accounts.v1"})
	for _, details := range []string{
		`[null]`,
		`[null,{"type":"unregistered.Detail","value":"AA"},{"type":"google.rpc.ErrorInfo","value":"AA"}]`,
		`[null,` + unknownReason + `,null,{"type":"unregistered.Detail","value":"AA"}]`,
	} {
		for _, method := range []string{"invoke", "lookup", "declare", "list"} {
			t.Run(method+details, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, `{"code":"failed_precondition","message":"diagnostic","details":%s}`, details)
				}))
				defer server.Close()
				c := datasource.New(gw{base: server.URL, client: server.Client()})
				receipt, err := parityCall(c, method)
				withReason := details == `[null,`+unknownReason+`,null,{"type":"unregistered.Detail","value":"AA"}]`
				if withReason && (method == "invoke" || method == "lookup") {
					var outcome *datasource.OutcomeUnknown
					if !errors.Is(err, datasource.ErrOutcomeUnknown) || !errors.As(err, &outcome) ||
						receipt == nil || receipt.EffectID == "" || outcome.Receipt.EffectID != receipt.EffectID {
						t.Fatalf("nil detail lost effect evidence: %+v, %v", receipt, err)
					}
				} else {
					var refused *datasource.OperationRefused
					wantReason := ""
					if withReason {
						wantReason = "SOURCE_OPERATION_OUTCOME_UNKNOWN"
					}
					if !errors.As(err, &refused) || refused.Reason != wantReason || errors.Is(err, datasource.ErrOutcomeUnknown) {
						t.Fatalf("detail/scope mapping = %T %v", err, err)
					}
				}
				if method == "invoke" && (receipt == nil || receipt.EffectID == "") {
					t.Fatal("invoke lost its minted ID")
				}
				if !connect.IsWireError(err) || connect.CodeOf(err) != connect.CodeFailedPrecondition || calls.Load() != 1 {
					t.Fatalf("wire identity lost or retried: %v, calls %d", err, calls.Load())
				}
			})
		}
	}
}

func TestConnectErrorEncodingParity(t *testing.T) {
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8", "application/json; charset=UTF-8", `application/json; charset="UtF-8"`} {
		for _, encoding := range []string{"", "gzip", "GZIP", "gZiP"} {
			for _, bom := range []bool{false, true} {
				for _, method := range []string{"invoke", "lookup", "declare", "list"} {
					t.Run(fmt.Sprintf("%s/%s/%s/bom=%t", method, contentType, encoding, bom), func(t *testing.T) {
						body := []byte(`{"code":"permission_denied","message":"host diagnostic"}`)
						if bom {
							body = append([]byte{0xef, 0xbb, 0xbf}, body...)
						}
						if encoding != "" {
							var compressed bytes.Buffer
							writer := gzip.NewWriter(&compressed)
							_, _ = writer.Write(body)
							_ = writer.Close()
							body = compressed.Bytes()
						}
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
							calls.Add(1)
							w.Header().Set("Content-Type", contentType)
							if encoding != "" {
								w.Header().Set("Content-Encoding", encoding)
							}
							w.WriteHeader(http.StatusForbidden)
							_, _ = w.Write(body)
						}))
						defer server.Close()
						receipt, err := parityCall(datasource.New(gw{base: server.URL, client: server.Client()}), method)
						if !errors.Is(err, datasource.ErrNotPermitted) || !connect.IsWireError(err) ||
							err.Error() != "permission_denied: host diagnostic" || calls.Load() != 1 {
							t.Fatalf("encoded error = %v, calls %d", err, calls.Load())
						}
						if method == "invoke" && (receipt == nil || receipt.EffectID == "") {
							t.Fatal("encoded error lost the effect ID")
						}
					})
				}
			}
		}
	}
}

func TestNonWireForbiddenHasMethodSpecificMapping(t *testing.T) {
	for _, tt := range []struct{ contentType, encoding, body string }{
		{"text/plain", "", "forbidden"}, {"application/json", "", ""},
		{"application/json; charset=iso-8859-1", "", `{"code":"permission_denied"}`},
		{"application/problem+json", "", `{"code":"permission_denied"}`},
		{"application/json", "", `[]`}, {"application/json", "", `{"code":123}`},
		{"application/json", "", `{"message":123}`}, {"application/json", "", `{} trailing`},
		{"application/json", "gzip", "corrupt gzip"}, {"application/json", "GZIP", "corrupt gzip"},
	} {
		for _, method := range []string{"invoke", "lookup", "declare", "list"} {
			t.Run(method+tt.contentType+tt.encoding+tt.body, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", tt.contentType)
					w.Header().Set("Content-Encoding", tt.encoding)
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(tt.body))
				}))
				defer server.Close()
				receipt, err := parityCall(datasource.New(gw{base: server.URL, client: server.Client()}), method)
				want := datasource.ErrNotPermitted
				if method == "invoke" || method == "lookup" {
					want = datasource.ErrOutcomeUnknown
					if receipt == nil || receipt.EffectID == "" || receipt.Status != datasource.ReceiptStatusUnknown {
						t.Fatalf("non-wire error lost the effect: %+v", receipt)
					}
				}
				if !errors.Is(err, want) || connect.IsWireError(err) {
					t.Fatalf("non-wire %s error = %v, want %v", method, err, want)
				}
			})
		}
	}
}

func TestUnsupportedResponseEncodingIsTypedBeforeHTTPFallback(t *testing.T) {
	for _, encoding := range []string{"br", "BR", "deflate", "gzip, br"} {
		for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
			for _, contentType := range []string{"application/json", "text/plain"} {
				for _, method := range []string{"invoke", "lookup", "declare", "list"} {
					t.Run(fmt.Sprintf("%s/%s/%s/%d", method, encoding, contentType, status), func(t *testing.T) {
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
							calls.Add(1)
							w.Header().Set("Content-Type", contentType)
							w.Header().Set("Content-Encoding", encoding)
							w.WriteHeader(status)
							fmt.Fprint(w, `{"code":"permission_denied","message":"not authoritative under unsupported encoding"}`)
						}))
						defer server.Close()
						receipt, err := parityCall(datasource.New(gw{base: server.URL, client: server.Client()}), method)
						var transport *connect.Error
						if !errors.As(err, &transport) || transport.Code() != connect.CodeInternal || connect.IsWireError(err) ||
							errors.Is(err, datasource.ErrNotPermitted) || calls.Load() != 1 {
							t.Fatalf("unsupported encoding inferred an outcome or retried: %v, calls %d", err, calls.Load())
						}
						var responseErr *datasource.DatasourceError
						if method == "declare" || method == "list" {
							if !errors.As(err, &responseErr) || responseErr.Unwrap() != transport ||
								receipt != nil || !reflect.DeepEqual(responseErr.Receipt, datasource.Receipt{}) {
								t.Fatalf("management encoding error = %T %v, receipt %+v", err, err, receipt)
							}
						} else {
							var unknown *datasource.OutcomeUnknown
							if !errors.As(err, &unknown) || errors.As(err, &responseErr) ||
								receipt == nil || receipt.EffectID == "" || receipt.Status != datasource.ReceiptStatusUnknown ||
								unknown.Receipt.EffectID != receipt.EffectID || unknown.Unwrap() != transport {
								t.Fatalf("encoding error lost effect evidence: %+v, %v", receipt, err)
							}
						}
					})
				}
			}
		}
	}
}

func TestCommittedOutputFieldsRetainTheirOwnEvidence(t *testing.T) {
	for _, tt := range []struct {
		name, top, nested string
		invalid           bool
	}{
		{"different", `{"top":1}`, `{"receipt":9007199254740993}`, false},
		{"missing top", "", `{"receipt":1}`, true},
		{"bad top", `[]`, `{"receipt":1}`, true},
		{"trailing top", `{}{}`, `{"receipt":1}`, true},
		{"nan top", `{"n":NaN}`, `{"receipt":1}`, true},
		{"missing receipt output", `{}`, "", true},
		{"bad receipt output", `{}`, `[]`, true},
		{"both bad", "nope", "nope", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			response := func(id string) *connect.Response[v1.InvokeSourceOperationResponse] {
				calls.Add(1)
				receipt := committedReceipt(id)
				receipt.OutputJson = tt.nested
				return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: tt.top, Receipt: receipt})
			}
			c := newOperationsClient(t, &operationsHandler{
				invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
					return response(req.Msg.EffectId), nil
				},
				lookup: func(_ context.Context, req *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
					return response(req.Msg.EffectId), nil
				},
			})
			result, invokeErr := c.Invoke(context.Background(), "org", "source", "read_item", map[string]any{})
			if result == nil || result.Receipt.EffectID == "" {
				t.Fatal("committed result lost its effect ID")
			}
			receipt, lookupErr := c.Lookup(context.Background(), "org", "source", result.Receipt.EffectID)
			for _, value := range []struct {
				receipt *datasource.Receipt
				err     error
			}{{&result.Receipt, invokeErr}, {receipt, lookupErr}} {
				if value.receipt == nil || value.receipt.Status != datasource.ReceiptStatusCommitted || value.receipt.CommittedAt.IsZero() || value.receipt.ProviderStatus != 200 {
					t.Fatalf("output erased commit evidence: %+v, %v", value.receipt, value.err)
				}
				var outputErr *datasource.DatasourceError
				if tt.invalid {
					if !errors.As(value.err, &outputErr) || errors.Is(value.err, datasource.ErrOutcomeUnknown) ||
						!reflect.DeepEqual(outputErr.Receipt, *value.receipt) || outputErr.Unwrap() == nil {
						t.Fatalf("invalid committed output = %T %v", value.err, value.err)
					}
				} else if value.err != nil || string(value.receipt.Output) != tt.nested {
					t.Fatalf("canonical receipt output = %s, %v", value.receipt.Output, value.err)
				}
				if json.Valid([]byte(tt.nested)) && tt.nested != "[]" && string(value.receipt.Output) != tt.nested {
					t.Fatal("valid canonical output was discarded")
				}
			}
			if !tt.invalid && string(result.Output) != tt.top {
				t.Fatal("result output must retain the top-level field")
			}
			if calls.Load() != 2 {
				t.Fatal("output failure retried the effect")
			}
		})
	}
}

func TestLostOrUndecodableReplyRetainsEffectID(t *testing.T) {
	for _, lost := range []bool{false, true} {
		for _, method := range []string{"invoke", "lookup"} {
			t.Run(fmt.Sprintf("%s/lost=%t", method, lost), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					if lost {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					w.Header().Set("Content-Type", "application/proto")
					_, _ = w.Write([]byte("not protobuf"))
				}))
				defer server.Close()
				receipt, err := parityCall(datasource.New(gw{base: server.URL, client: server.Client()}), method)
				var unknown *datasource.OutcomeUnknown
				if !errors.As(err, &unknown) || receipt == nil || receipt.EffectID == "" || receipt.Status != datasource.ReceiptStatusUnknown ||
					unknown.Receipt.EffectID != receipt.EffectID || unknown.Unwrap() == nil || calls.Load() != 1 {
					t.Fatalf("lost/undecodable reply lost evidence or retried: %+v, %v, calls %d", receipt, err, calls.Load())
				}
			})
		}
	}
}

func TestMissingSchemasRefuseDeclareAndNormalizeList(t *testing.T) {
	var declares atomic.Int32
	c := newOperationsClient(t, &operationsHandler{
		declare: func(_ context.Context, req *connect.Request[v1.DeclareSourceOperationsRequest]) (*connect.Response[v1.DeclareSourceOperationsResponse], error) {
			declares.Add(1)
			for _, operation := range req.Msg.Operations {
				if operation.InputSchema == nil || operation.OutputSchema == nil {
					t.Error("missing schema was dispatched")
				}
			}
			return connect.NewResponse(&v1.DeclareSourceOperationsResponse{}), nil
		},
		list: func(context.Context, *connect.Request[v1.ListSourceOperationsRequest]) (*connect.Response[v1.ListSourceOperationsResponse], error) {
			return connect.NewResponse(&v1.ListSourceOperationsResponse{Operations: []*v1.SourceOperation{{Name: "read_item", Effect: v1.SourceOperation_EFFECT_READ_ONLY}}}), nil
		},
	})
	for _, operation := range []datasource.Operation{
		{Effect: datasource.EffectReadOnly},
		{Input: json.RawMessage(`{}`), Effect: datasource.EffectReadOnly},
		{Output: json.RawMessage(`{}`), Effect: datasource.EffectReadOnly},
	} {
		var inputErr *datasource.InputError
		if err := c.DeclareOperations(context.Background(), "org", "source", []datasource.Operation{operation}); !errors.As(err, &inputErr) || declares.Load() != 0 {
			t.Fatalf("missing schemas dispatched or untyped: %v, calls %d", err, declares.Load())
		}
	}
	operations, err := c.ListOperations(context.Background(), "org", "source")
	if err != nil || len(operations) != 1 || string(operations[0].Input) != "{}" || string(operations[0].Output) != "{}" {
		t.Fatalf("unset listed schemas = %+v, %v", operations, err)
	}
	if err := c.DeclareOperations(context.Background(), "org", "source", operations); err != nil || declares.Load() != 1 {
		t.Fatalf("explicit empty schemas did not redeclare: %v", err)
	}
}

func checkInvalidListedEffect(t *testing.T, operations []datasource.Operation, err error, unspecified bool, calls int32) {
	t.Helper()
	if operations != nil || calls != 1 {
		t.Fatalf("invalid listed effect returned partial data or retried: %+v, %v, calls %d", operations, err, calls)
	}
	var inputErr *datasource.InputError
	var responseErr *datasource.DatasourceError
	if unspecified {
		if !errors.As(err, &inputErr) || inputErr.Pointer != "" || inputErr.Error() != "host returned an unspecified effect" ||
			inputErr.Unwrap() == nil || errors.As(err, &responseErr) {
			t.Fatalf("unspecified listed effect = %T %v", err, err)
		}
	} else if !errors.As(err, &responseErr) || responseErr.Unwrap() == nil ||
		!reflect.DeepEqual(responseErr.Receipt, datasource.Receipt{}) || errors.As(err, &inputErr) {
		t.Fatalf("unrecognized listed effect = %T %v", err, err)
	}
}

func TestInvalidListedEffectIsTyped(t *testing.T) {
	for _, jsonCodec := range []bool{false, true} {
		for _, effect := range []v1.SourceOperation_Effect{v1.SourceOperation_EFFECT_UNSPECIFIED, 42} {
			t.Run(fmt.Sprintf("json=%t/effect=%d", jsonCodec, effect), func(t *testing.T) {
				var calls atomic.Int32
				var opts []connect.ClientOption
				if jsonCodec {
					opts = append(opts, connect.WithProtoJSON())
				}
				c := newOperationsClient(t, &operationsHandler{
					list: func(context.Context, *connect.Request[v1.ListSourceOperationsRequest]) (*connect.Response[v1.ListSourceOperationsResponse], error) {
						calls.Add(1)
						return connect.NewResponse(&v1.ListSourceOperationsResponse{Operations: []*v1.SourceOperation{
							{Name: "valid", Effect: v1.SourceOperation_EFFECT_READ_ONLY},
							{Name: "invalid", Effect: effect},
						}}), nil
					},
				}, opts...)
				operations, err := c.ListOperations(context.Background(), "org", "source")
				checkInvalidListedEffect(t, operations, err, effect == v1.SourceOperation_EFFECT_UNSPECIFIED, calls.Load())
			})
		}
	}
}

func TestUnspecifiedListedEffectJSONRepresentations(t *testing.T) {
	for _, operation := range []string{`{}`, `{"effect":0}`, `{"effect":"EFFECT_UNSPECIFIED"}`} {
		t.Run(operation, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"operations":[%s]}`, operation)
			}))
			defer server.Close()
			c := datasource.New(gw{base: server.URL, client: server.Client()}, connect.WithProtoJSON())
			operations, err := c.ListOperations(context.Background(), "org", "source")
			checkInvalidListedEffect(t, operations, err, true, calls.Load())
		})
	}
}
