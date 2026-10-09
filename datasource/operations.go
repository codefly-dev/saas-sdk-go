package datasource

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
)

// Invoke calls one host-declared operation once, through the gateway as the
// person. The host owns provider credentials, routing, replay and the receipt.
// On an RPC error the returned Result still carries the effect ID. An unknown
// outcome must be looked up, not interpreted as permission to repeat an effect.
// OperationDeclarationRemoved also requires Lookup with the original ID;
// its receipt status never authorizes recovery with a fresh ID.
func (c *Client) Invoke(ctx context.Context, orgID, sourceID, operation string, input any, opts ...InvokeOption) (*Result, error) {
	value, err := inputJSON(input)
	if err != nil {
		return nil, err
	}
	var options invokeOptions
	for _, option := range opts {
		option(&options)
	}
	if options.effectIDSet {
		if err := validateEffectID(options.effectID); err != nil {
			return nil, err
		}
	} else {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("datasource: mint effect ID: %w", err)
		}
		options.effectID = id.String()
	}
	if options.deadlineSet {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, options.deadline)
		defer cancel()
	}
	if err := operationContextError(ctx); err != nil {
		return nil, err
	}
	result := &Result{Receipt: Receipt{EffectID: options.effectID, Status: ReceiptStatusUnknown}}
	request := connect.NewRequest(&v1.InvokeSourceOperationRequest{
		OrgId: orgID, SourceId: sourceID, Operation: operation, InputJson: value, EffectId: options.effectID,
	})
	request.Header().Set("x-codefly-effect-id", options.effectID)
	response, err := c.inner.InvokeSourceOperation(ctx, request)
	if err != nil {
		mapped := mapHostError(err, true, time.Now())
		var unknown *OutcomeUnknown
		if errors.As(mapped, &unknown) {
			unknown.Receipt = result.Receipt
		}
		var removed *OperationDeclarationRemoved
		if errors.As(mapped, &removed) {
			result.Receipt.Status = removed.ReceiptStatus
			removed.Receipt = result.Receipt
		}
		return result, mapped
	}
	receipt, err := operationReceipt(response.Msg.GetReceipt(), options.effectID)
	if err != nil {
		return result, &OutcomeUnknown{Receipt: result.Receipt, cause: err}
	}
	result.Receipt = *receipt
	if receipt.Status == ReceiptStatusUnknown {
		return result, &OutcomeUnknown{Receipt: *receipt}
	}
	result.Output, err = objectJSON(response.Msg.GetOutputJson())
	result.Receipt.Output = result.Output
	return result, err
}

// Lookup reads the host's receipt without invoking the provider. A receipt with
// unknown outcome is returned alongside ErrOutcomeUnknown (errors.Is works).
// An unavailable lookup also leaves the outcome unknown; it is never a reason
// to dispatch the effect again.
// A NotFound response means no attempt exists and returns ErrEffectNotFound
// with the original ID for an explicit same-ID invoke. Saved receipts survive
// declaration removal or replacement; current source read authority still applies.
// orgID and sourceID preserve the shared SDK signature; the host derives their
// authority from the gateway's Work Context and the stored effect binding.
func (c *Client) Lookup(ctx context.Context, orgID, sourceID, effectID string) (*Receipt, error) {
	if err := validateEffectID(effectID); err != nil {
		return nil, err
	}
	if err := operationContextError(ctx); err != nil {
		return nil, err
	}
	response, err := c.inner.LookupInvokeSourceOperation(ctx, connect.NewRequest(&v1.LookupInvokeSourceOperationRequest{
		EffectId: effectID,
	}))
	if err != nil {
		if !connect.IsWireError(err) {
			receipt := &Receipt{EffectID: effectID, Status: ReceiptStatusUnknown}
			return receipt, &OutcomeUnknown{Receipt: *receipt, cause: err}
		}
		if connect.CodeOf(err) == connect.CodeNotFound {
			return &Receipt{EffectID: effectID, Status: ReceiptStatusNotAttempted}, &hostError{kind: ErrEffectNotFound, cause: err}
		}
		mapped := mapHostError(err, connect.CodeOf(err) == connect.CodeUnavailable, time.Now())
		var unknown *OutcomeUnknown
		if errors.As(mapped, &unknown) {
			unknown.Receipt = Receipt{EffectID: effectID, Status: ReceiptStatusUnknown}
			return &unknown.Receipt, mapped
		}
		var removed *OperationDeclarationRemoved
		if errors.As(mapped, &removed) {
			removed.Receipt.EffectID = effectID
			return &removed.Receipt, mapped
		}
		return nil, mapped
	}
	receipt, err := operationReceipt(response.Msg.GetReceipt(), effectID)
	if err != nil {
		receipt = &Receipt{EffectID: effectID, Status: ReceiptStatusUnknown}
		return receipt, &OutcomeUnknown{Receipt: *receipt, cause: err}
	}
	if receipt.Status == ReceiptStatusUnknown {
		return receipt, &OutcomeUnknown{Receipt: *receipt}
	}
	receipt.Output, err = objectJSON(response.Msg.GetOutputJson())
	return receipt, err
}

// DeclareOperations asks the host to replace the source's declarations
// atomically. The host validates the set and enforces administrator permission.
func (c *Client) DeclareOperations(ctx context.Context, orgID, sourceID string, operations []Operation) error {
	declared := make([]*v1.SourceOperation, 0, len(operations))
	for _, operation := range operations {
		input, err := schemaStruct(operation.Input)
		if err != nil {
			return err
		}
		output, err := schemaStruct(operation.Output)
		if err != nil {
			return err
		}
		var effect v1.SourceOperation_Effect
		switch operation.Effect {
		case EffectReadOnly:
			effect = v1.SourceOperation_EFFECT_READ_ONLY
		case EffectMutation:
			effect = v1.SourceOperation_EFFECT_MUTATION
		default:
			return &InputError{cause: fmt.Errorf("datasource: invalid operation effect %q", operation.Effect)}
		}
		declared = append(declared, &v1.SourceOperation{
			Name: operation.Name, Description: operation.Description, Method: operation.Method, Path: operation.Path, Query: operation.Query,
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
		input, err := schemaJSON(value.GetInputSchema())
		if err != nil {
			return nil, err
		}
		output, err := schemaJSON(value.GetOutputSchema())
		if err != nil {
			return nil, err
		}
		var effect Effect
		switch value.GetEffect() {
		case v1.SourceOperation_EFFECT_READ_ONLY:
			effect = EffectReadOnly
		case v1.SourceOperation_EFFECT_MUTATION:
			effect = EffectMutation
		default:
			return nil, fmt.Errorf("datasource: unsupported host operation effect %v", value.GetEffect())
		}
		operations = append(operations, Operation{
			Name: value.GetName(), Description: value.GetDescription(), Digest: value.GetDigest(),
			Method: value.GetMethod(), Path: value.GetPath(), Query: value.GetQuery(),
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
	case "committed":
		receipt.Status = ReceiptStatusCommitted
	case "unknown":
		receipt.Status = ReceiptStatusUnknown
	default:
		return nil, fmt.Errorf("datasource: unsupported host receipt status %v", value.GetStatus())
	}
	if committed := value.GetCommittedAt(); committed != "" {
		var err error
		receipt.CommittedAt, err = time.Parse(time.RFC3339Nano, committed)
		if err != nil {
			return nil, fmt.Errorf("datasource: invalid receipt commit time: %w", err)
		}
	}
	return receipt, nil
}

func validateEffectID(id string) error {
	valid := len(id) > 0 && len(id) <= 128 && utf8.ValidString(id)
	// net/http trims surrounding ASCII spaces from header values, which would
	// make the effect header differ from the protobuf field at the host.
	if valid {
		valid = id[0] != ' ' && id[len(id)-1] != ' '
	}
	for i := 0; i < len(id) && valid; i++ {
		valid = id[i] >= 0x20 && id[i] != 0x7f
	}
	if !valid {
		return &InputError{cause: errors.New("datasource: effect ID must be 1–128 UTF-8 bytes without control bytes or surrounding spaces")}
	}
	return nil
}

func operationContextError(ctx context.Context) error {
	err := ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return &InputError{cause: fmt.Errorf("datasource: deadline expired before dispatch: %w", err)}
	}
	return err
}
