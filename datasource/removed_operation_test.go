package datasource_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/codefly-dev/saas-sdk-go/datasource"
	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
)

func declarationRemovedError(t *testing.T, metadata map[string]string) *connect.Error {
	t.Helper()
	err := connect.NewError(connect.CodeFailedPrecondition, errors.New("source operation declaration was removed; recover the original effect with Lookup"))
	detail, detailErr := connect.NewErrorDetail(&errdetails.ErrorInfo{
		Reason: "SOURCE_OPERATION_DECLARATION_REMOVED", Domain: "saas.accounts.v1", Metadata: metadata,
	})
	if detailErr != nil {
		t.Fatal(detailErr)
	}
	err.AddDetail(detail)
	return err
}

func TestOperationDeclarationRemovedOverConnect(t *testing.T) {
	for _, tt := range []struct {
		name     string
		metadata map[string]string
		status   datasource.ReceiptStatus
	}{
		{"committed", map[string]string{"receipt_status": "committed"}, datasource.ReceiptStatusCommitted},
		{"unknown", map[string]string{"receipt_status": "unknown"}, datasource.ReceiptStatusUnknown},
		{"missing", nil, datasource.ReceiptStatusUnknown},
		{"empty", map[string]string{"receipt_status": ""}, datasource.ReceiptStatusUnknown},
		{"uppercase", map[string]string{"receipt_status": "COMMITTED"}, datasource.ReceiptStatusUnknown},
		{"whitespace", map[string]string{"receipt_status": " committed "}, datasource.ReceiptStatusUnknown},
		{"not attempted", map[string]string{"receipt_status": "not_attempted"}, datasource.ReceiptStatusUnknown},
		{"unrecognized", map[string]string{"receipt_status": "future"}, datasource.ReceiptStatusUnknown},
	} {
		for _, suppliedID := range []bool{false, true} {
			for _, jsonCodec := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/provided=%t/json=%t", tt.name, suppliedID, jsonCodec), func(t *testing.T) {
					var invokes, lookups atomic.Int32
					hostError := declarationRemovedError(t, tt.metadata)
					var options []connect.ClientOption
					if jsonCodec {
						options = append(options, connect.WithProtoJSON())
					}
					var sentID atomic.Value
					c := newOperationsClient(t, &operationsHandler{
						invoke: func(_ context.Context, req *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
							invokes.Add(1)
							sentID.Store(req.Msg.GetEffectId())
							return nil, hostError
						},
						lookup: func(context.Context, *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
							lookups.Add(1)
							// Defensive classification: this reason normally comes from Invoke.
							return nil, hostError
						},
					}, options...)
					var invokeOptions []datasource.InvokeOption
					if suppliedID {
						invokeOptions = append(invokeOptions, datasource.WithEffectID("persisted-effect"))
					}
					result, err := c.Invoke(context.Background(), "org", "source", "create_invoice", map[string]any{}, invokeOptions...)
					outcome := checkDeclarationRemoved(t, err, tt.status, hostError.Error())
					effectID, _ := sentID.Load().(string)
					if result == nil || effectID == "" || (suppliedID && effectID != "persisted-effect") ||
						!reflect.DeepEqual(result.Receipt, datasource.Receipt{EffectID: effectID, Status: tt.status}) ||
						!reflect.DeepEqual(outcome.Receipt, result.Receipt) || result.Output != nil {
						t.Fatalf("removed outcome lost the ID/status or invented evidence: %+v, %+v", result, outcome)
					}
					if invokes.Load() != 1 || lookups.Load() != 0 {
						t.Fatal("removed outcome triggered an automatic retry or lookup")
					}
					receipt, err := c.Lookup(context.Background(), "org", "source", effectID)
					outcome = checkDeclarationRemoved(t, err, tt.status, hostError.Error())
					if receipt == nil || !reflect.DeepEqual(*receipt, result.Receipt) ||
						!reflect.DeepEqual(*receipt, outcome.Receipt) || invokes.Load() != 1 || lookups.Load() != 1 {
						t.Fatalf("lookup lost evidence or reinvoked: %+v, %v", receipt, err)
					}
				})
			}
		}
	}
}

func checkDeclarationRemoved(t *testing.T, err error, status datasource.ReceiptStatus, message string) *datasource.OperationDeclarationRemoved {
	t.Helper()
	var outcome *datasource.OperationDeclarationRemoved
	var provider *datasource.ProviderRefused
	var refused *datasource.OperationRefused
	var transport *connect.Error
	if !errors.Is(err, datasource.ErrOperationDeclarationRemoved) || !errors.As(err, &outcome) ||
		outcome.ReceiptStatus != status || outcome.Receipt.Status != status ||
		errors.Is(err, datasource.ErrOutcomeUnknown) || errors.Is(err, datasource.ErrUnknownOperation) ||
		errors.Is(err, datasource.ErrEffectNotFound) || errors.As(err, &provider) || errors.As(err, &refused) {
		t.Fatalf("removed outcome misclassified: %T %+v", err, err)
	}
	if !errors.As(err, &transport) || connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		outcome.Unwrap() != transport || err.Error() != message {
		t.Fatalf("removed outcome lost host diagnostics: %v", err)
	}
	if !errors.Is(fmt.Errorf("invoke: %w", err), datasource.ErrOperationDeclarationRemoved) {
		t.Fatal("wrapped outcome lost its sentinel")
	}
	return outcome
}

func TestRemovedDeclarationRecoveryUsesLookup(t *testing.T) {
	for _, state := range []string{"committed", "unresolved", "expired", "revoked access"} {
		t.Run(state, func(t *testing.T) {
			var invokes, lookups atomic.Int32
			const effectID = "original-effect"
			const output = `{"id":18446744073709551615}`
			status := "unknown"
			if state == "committed" || state == "revoked access" {
				status = "committed"
			}
			c := newOperationsClient(t, &operationsHandler{
				invoke: func(context.Context, *connect.Request[v1.InvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
					invokes.Add(1)
					return nil, declarationRemovedError(t, map[string]string{"receipt_status": status})
				},
				lookup: func(_ context.Context, req *connect.Request[v1.LookupInvokeSourceOperationRequest]) (*connect.Response[v1.InvokeSourceOperationResponse], error) {
					lookups.Add(1)
					if req.Msg.GetEffectId() != effectID {
						t.Error("recovery changed the effect ID")
					}
					if state == "revoked access" {
						return nil, connect.NewError(connect.CodePermissionDenied, errors.New("source read access revoked"))
					}
					if state != "committed" {
						return connect.NewResponse(&v1.InvokeSourceOperationResponse{Receipt: &v1.SourceOperationReceipt{EffectId: effectID, Status: "unknown"}}), nil
					}
					receipt := committedReceipt(effectID)
					receipt.OutputJson = output
					return connect.NewResponse(&v1.InvokeSourceOperationResponse{OutputJson: output, Receipt: receipt}), nil
				},
			})
			result, err := c.Invoke(context.Background(), "org", "source", "create_invoice", map[string]any{}, datasource.WithEffectID(effectID))
			if !errors.Is(err, datasource.ErrOperationDeclarationRemoved) || result == nil {
				t.Fatalf("removed declaration = %+v, %v", result, err)
			}
			receipt, err := c.Lookup(context.Background(), "org", "source", result.Receipt.EffectID)
			switch state {
			case "committed":
				if err != nil || receipt == nil || receipt.Status != datasource.ReceiptStatusCommitted || string(receipt.Output) != output {
					t.Fatalf("saved commit = %+v, %v", receipt, err)
				}
			case "revoked access":
				if !errors.Is(err, datasource.ErrNotPermitted) || receipt != nil {
					t.Fatalf("revoked access = %+v, %v", receipt, err)
				}
			default:
				if !errors.Is(err, datasource.ErrOutcomeUnknown) || receipt == nil || receipt.Status != datasource.ReceiptStatusUnknown || receipt.Output != nil {
					t.Fatalf("unresolved/expired receipt = %+v, %v", receipt, err)
				}
			}
			if invokes.Load() != 1 || lookups.Load() != 1 {
				t.Fatal("receipt recovery retried or reinvoked")
			}
		})
	}
}
