package datasource

import (
	"errors"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

var (
	ErrNotPermitted     = errors.New("datasource: not permitted")
	ErrUnknownOperation = errors.New("datasource: unknown operation")
	// ErrEffectNotFound is Lookup's absent-effect outcome. A caller may explicitly
	// invoke again with the same effect ID; the SDK never does so automatically.
	ErrEffectNotFound = errors.New("datasource: effect not found")
	// ErrEffectReused means the host rejected changed input under an existing ID.
	ErrEffectReused = errors.New("datasource: effect ID reused")
	// ErrOperationDeclarationRemoved means a retained effect's declaration was
	// removed. Recover with Lookup under the original ID; never mint a fresh ID.
	ErrOperationDeclarationRemoved = errors.New("datasource: operation declaration removed; look up the original effect")
	// ErrOutcomeUnknown means the effect may have happened. Inspect the receipt
	// with Lookup; never interpret this outcome as permission to repeat it.
	ErrOutcomeUnknown = errors.New("datasource: outcome unknown; look up the effect")
)

// DatasourceError reports invalid output accompanying a valid committed receipt.
// Receipt preserves the commit evidence and any valid receipt output. This is
// not an unknown effect or permission to retry it, especially under a fresh ID.
type DatasourceError struct {
	Receipt Receipt
	cause   error
}

func (e *DatasourceError) Error() string {
	if e.cause == nil {
		return "datasource: invalid committed output"
	}
	return e.cause.Error()
}
func (e *DatasourceError) Unwrap() error { return e.cause }

// InputError reports a rejected input. Pointer is the host's JSON Pointer from
// BadRequest.FieldViolation.Field, or empty when no structured pointer is supplied.
type InputError struct {
	Pointer string
	cause   error
}

func (e *InputError) Error() string {
	if e.cause == nil {
		return "datasource: invalid input at " + e.Pointer
	}
	return e.cause.Error()
}
func (e *InputError) Unwrap() error { return e.cause }

// RateLimited reports the datasource budget's next reset, without scheduling a
// retry. ResetAt is zero when the host supplies no valid reset or retry delay.
type RateLimited struct {
	ResetAt time.Time
	cause   error
}

func (e *RateLimited) Error() string {
	if e.cause == nil {
		return "datasource: rate limited"
	}
	return e.cause.Error()
}
func (e *RateLimited) Unwrap() error { return e.cause }

// ProviderRefused reports SOURCE_PROVIDER_REFUSED. Status is the HTTP status
// from the host's provider_status metadata, or zero when absent or invalid.
type ProviderRefused struct {
	Status int
	cause  error
}

func (e *ProviderRefused) Error() string {
	if e.cause == nil {
		return "datasource: provider refused"
	}
	return e.cause.Error()
}
func (e *ProviderRefused) Unwrap() error { return e.cause }

// OperationRefused reports a host precondition refusal. Reason is the structured
// ErrorInfo reason, or empty when the host supplies none; prose is not parsed.
// A refusal of this request does not establish the outcome of an earlier attempt.
type OperationRefused struct {
	Reason string
	cause  error
}

func (e *OperationRefused) Error() string {
	if e.cause == nil {
		return "datasource: operation refused"
	}
	return e.cause.Error()
}
func (e *OperationRefused) Unwrap() error { return e.cause }

// OperationDeclarationRemoved reports retained effect evidence after its
// declaration was removed. ReceiptStatus and Receipt.Status carry the host's
// observation: COMMITTED or UNKNOWN (also used for missing/invalid metadata).
// This outcome has no output, commit time or provider status; recover those
// with Lookup using Receipt.EffectID. Never recover with a fresh ID.
type OperationDeclarationRemoved struct {
	ReceiptStatus ReceiptStatus
	Receipt       Receipt
	cause         error
}

func (e *OperationDeclarationRemoved) Error() string {
	if e.cause == nil {
		return ErrOperationDeclarationRemoved.Error()
	}
	return e.cause.Error()
}
func (e *OperationDeclarationRemoved) Is(target error) bool {
	return target == ErrOperationDeclarationRemoved
}
func (e *OperationDeclarationRemoved) Unwrap() error { return e.cause }

type hostError struct {
	kind  error
	cause error
}

func (e *hostError) Error() string        { return e.cause.Error() }
func (e *hostError) Unwrap() error        { return e.cause }
func (e *hostError) Is(target error) bool { return target == e.kind }

// OutcomeUnknown means the provider may have acted. Receipt retains the effect
// ID and Unwrap preserves the original diagnostic for errors.Is/errors.As.
// Check errors.Is(err, ErrOutcomeUnknown) before classifying transport errors:
// the wrapped transport code is not permission to retry the effect.
type OutcomeUnknown struct {
	Receipt Receipt
	cause   error
}

func (e *OutcomeUnknown) Error() string {
	if e.cause == nil {
		return ErrOutcomeUnknown.Error()
	}
	return e.cause.Error()
}
func (e *OutcomeUnknown) Is(target error) bool { return target == ErrOutcomeUnknown }
func (e *OutcomeUnknown) Unwrap() error        { return e.cause }

// Cause returns the original diagnostic, if any. Its transport code does not
// establish whether the effect happened and must not drive an effect retry.
func (e *OutcomeUnknown) Cause() error { return e.cause }

// mapHostError does not infer provider status or retry policy from free text.
// A transport loss during Invoke is conservatively unknown: this client does
// not cache declarations and cannot prove that an operation is read-only.
func mapHostError(err error, effectOutcome bool, now time.Time) error {
	if err == nil {
		return nil
	}
	// HTTP status alone cannot establish whether the host attempted an effect.
	// Use Connect's decoded-wire marker, not its inferred HTTP-to-code mapping:
	// for example, a proxy's plain 429 and 500 otherwise produce different codes.
	if effectOutcome && !connect.IsWireError(err) {
		return &OutcomeUnknown{cause: err}
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return err
	}
	var info *errdetails.ErrorInfo
	var retry *errdetails.RetryInfo
	var bad *errdetails.BadRequest
	for _, detail := range ce.Details() {
		if detail == nil {
			continue
		}
		value, detailErr := detail.Value()
		if detailErr != nil {
			continue
		}
		switch value := value.(type) {
		case *errdetails.ErrorInfo:
			info = value
		case *errdetails.RetryInfo:
			retry = value
		case *errdetails.BadRequest:
			bad = value
		}
	}
	switch ce.Code() {
	case connect.CodePermissionDenied:
		return &hostError{kind: ErrNotPermitted, cause: err}
	case connect.CodeNotFound:
		return &hostError{kind: ErrUnknownOperation, cause: err}
	case connect.CodeInvalidArgument:
		pointer := ""
		if fields := bad.GetFieldViolations(); len(fields) > 0 {
			pointer = fields[0].GetField()
		}
		return &InputError{Pointer: pointer, cause: err}
	case connect.CodeResourceExhausted:
		if info.GetReason() != "DATASOURCE_RATE_LIMITED" {
			return err
		}
		reset, parseErr := parseHostTime(info.GetMetadata()["reset_at"])
		if parseErr != nil {
			reset = time.Time{}
			if delay := retry.GetRetryDelay(); delay != nil && delay.CheckValid() == nil && delay.AsDuration() >= 0 {
				reset = now.Add(delay.AsDuration())
			}
		}
		return &RateLimited{ResetAt: reset, cause: err}
	case connect.CodeFailedPrecondition:
		switch info.GetReason() {
		case "SOURCE_OPERATION_OUTCOME_UNKNOWN":
			if !effectOutcome {
				return &OperationRefused{Reason: info.GetReason(), cause: err}
			}
			return &OutcomeUnknown{cause: err}
		case "SOURCE_OPERATION_DECLARATION_REMOVED":
			status := ReceiptStatusUnknown
			if info.GetMetadata()["receipt_status"] == "committed" {
				status = ReceiptStatusCommitted
			}
			return &OperationDeclarationRemoved{ReceiptStatus: status, Receipt: Receipt{Status: status}, cause: err}
		case "SOURCE_PROVIDER_REFUSED":
			encoded := info.GetMetadata()["provider_status"]
			status, parseErr := strconv.Atoi(encoded)
			// The host emits canonical decimal HTTP statuses from 100 through 599.
			if parseErr != nil || status < 100 || status > 599 || strconv.Itoa(status) != encoded {
				status = 0
			}
			return &ProviderRefused{Status: status, cause: err}
		case "SOURCE_EFFECT_REUSED":
			return &hostError{kind: ErrEffectReused, cause: err}
		default:
			return &OperationRefused{Reason: info.GetReason(), cause: err}
		}
	case connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeCanceled:
		if effectOutcome {
			return &OutcomeUnknown{cause: err}
		}
	}
	return err
}
