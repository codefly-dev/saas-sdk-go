package datasource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
)

// Invoke calls one host-declared operation once, through the gateway as the
// person. The host owns provider credentials, routing, replay and the receipt.
// On an RPC error the returned Result still carries the effect ID. An unknown
// outcome must be looked up, not interpreted as permission to repeat an effect.
func (c *Client) Invoke(ctx context.Context, orgID, sourceID, operation string, input any, opts ...InvokeOption) (*Result, error) {
	value, err := inputStruct(input)
	if err != nil {
		return nil, err
	}
	var options invokeOptions
	for _, option := range opts {
		option(&options)
	}
	if options.effectID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("datasource: mint effect ID: %w", err)
		}
		options.effectID = id.String()
	}
	if !options.deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, options.deadline)
		defer cancel()
	}
	result := &Result{Receipt: Receipt{EffectID: options.effectID, Status: ReceiptStatusUnknown}}
	request := connect.NewRequest(&v1.InvokeSourceOperationRequest{
		OrgId: orgID, SourceId: sourceID, Operation: operation, Input: value, EffectId: options.effectID,
	})
	request.Header().Set("x-codefly-effect-id", options.effectID)
	response, err := c.inner.InvokeSourceOperation(ctx, request)
	if err != nil {
		return result, mapHostError(err, true, time.Now())
	}
	receipt, err := operationReceipt(response.Msg.GetReceipt(), options.effectID)
	if err != nil {
		return result, &unknownOutcome{cause: err}
	}
	result.Receipt = *receipt
	if receipt.Status == ReceiptStatusUnknown {
		return result, ErrOutcomeUnknown
	}
	result.Output, err = structJSON(response.Msg.GetOutput())
	return result, err
}

// Lookup reads the host's receipt without invoking the provider. A receipt with
// unknown outcome is returned alongside ErrOutcomeUnknown (errors.Is works).
// An unavailable lookup also leaves the outcome unknown; it is never a reason
// to dispatch the effect again.
func (c *Client) Lookup(ctx context.Context, orgID, sourceID, effectID string) (*Receipt, error) {
	response, err := c.inner.LookupInvokeSourceOperation(ctx, connect.NewRequest(&v1.LookupInvokeSourceOperationRequest{
		OrgId: orgID, SourceId: sourceID, EffectId: effectID,
	}))
	if err != nil {
		mapped := mapHostError(err, connect.CodeOf(err) == connect.CodeUnavailable, time.Now())
		if errors.Is(mapped, ErrOutcomeUnknown) {
			return &Receipt{EffectID: effectID, Status: ReceiptStatusUnknown}, mapped
		}
		return nil, mapped
	}
	receipt, err := operationReceipt(response.Msg.GetReceipt(), effectID)
	if err != nil {
		return nil, err
	}
	if receipt.Status == ReceiptStatusUnknown {
		return receipt, ErrOutcomeUnknown
	}
	return receipt, nil
}

// DeclareOperations asks the host to replace the source's declarations
// atomically. The host validates the set and enforces administrator permission.
func (c *Client) DeclareOperations(ctx context.Context, orgID, sourceID string, operations []Operation) error {
	declared := make([]*v1.SourceOperation, 0, len(operations))
	for _, operation := range operations {
		input, err := jsonStruct(operation.Input)
		if err != nil {
			return err
		}
		output, err := jsonStruct(operation.Output)
		if err != nil {
			return err
		}
		var effect v1.SourceOperation_Effect
		switch operation.Effect {
		case EffectReadOnly:
			effect = v1.SourceOperation_READ_ONLY
		case EffectMutation:
			effect = v1.SourceOperation_MUTATION
		default:
			return &InputError{cause: fmt.Errorf("datasource: invalid operation effect %q", operation.Effect)}
		}
		declared = append(declared, &v1.SourceOperation{
			Name: operation.Name, Method: operation.Method, Path: operation.Path, Query: operation.Query,
			InputSchema: input, OutputSchema: output, Effect: effect, MaxOutputBytes: operation.MaxOutputBytes,
		})
	}
	_, err := c.inner.DeclareSourceOperations(ctx, connect.NewRequest(&v1.DeclareSourceOperationsRequest{
		OrgId: orgID, SourceId: sourceID, Operations: declared,
	}))
	return mapHostError(err, false, time.Now())
}

// ListOperations reads the host's admitted set without retaining a local copy.
func (c *Client) ListOperations(ctx context.Context, orgID, sourceID string) ([]Operation, error) {
	response, err := c.inner.ListSourceOperations(ctx, connect.NewRequest(&v1.ListSourceOperationsRequest{
		OrgId: orgID, SourceId: sourceID,
	}))
	if err != nil {
		return nil, mapHostError(err, false, time.Now())
	}
	operations := make([]Operation, 0, len(response.Msg.GetOperations()))
	for _, value := range response.Msg.GetOperations() {
		input, err := structJSON(value.GetInputSchema())
		if err != nil {
			return nil, err
		}
		output, err := structJSON(value.GetOutputSchema())
		if err != nil {
			return nil, err
		}
		var effect Effect
		switch value.GetEffect() {
		case v1.SourceOperation_READ_ONLY:
			effect = EffectReadOnly
		case v1.SourceOperation_MUTATION:
			effect = EffectMutation
		default:
			return nil, fmt.Errorf("datasource: unsupported host operation effect %v", value.GetEffect())
		}
		operations = append(operations, Operation{
			Name: value.GetName(), Method: value.GetMethod(), Path: value.GetPath(), Query: value.GetQuery(),
			Input: input, Output: output, Effect: effect, MaxOutputBytes: value.GetMaxOutputBytes(),
		})
	}
	return operations, nil
}

func operationReceipt(value *v1.SourceOperationReceipt, effectID string) (*Receipt, error) {
	if value == nil || value.GetEffectId() != effectID {
		return nil, errors.New("datasource: host returned a missing or mismatched receipt")
	}
	receipt := &Receipt{EffectID: effectID, ProviderStatus: int(value.GetProviderStatus())}
	switch value.GetStatus() {
	case v1.SourceOperationReceiptStatus_SOURCE_OPERATION_RECEIPT_STATUS_COMMITTED:
		receipt.Status = ReceiptStatusCommitted
	case v1.SourceOperationReceiptStatus_SOURCE_OPERATION_RECEIPT_STATUS_REFUSED:
		receipt.Status = ReceiptStatusRefused
	case v1.SourceOperationReceiptStatus_SOURCE_OPERATION_RECEIPT_STATUS_UNKNOWN:
		receipt.Status = ReceiptStatusUnknown
	default:
		return nil, fmt.Errorf("datasource: unsupported host receipt status %v", value.GetStatus())
	}
	if committed := value.GetCommittedAt(); committed != nil {
		if err := committed.CheckValid(); err != nil {
			return nil, fmt.Errorf("datasource: invalid receipt commit time: %w", err)
		}
		receipt.CommittedAt = committed.AsTime()
	}
	return receipt, nil
}
