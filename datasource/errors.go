package datasource

import (
	"errors"
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
	// ErrOutcomeUnknown means the effect may have happened. Inspect the receipt
	// with Lookup; never interpret this outcome as permission to repeat it.
	ErrOutcomeUnknown = errors.New("datasource: outcome unknown; look up the effect")
)

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

// ProviderRefused reports SOURCE_PROVIDER_REFUSED. Status is reserved and stays
// zero: the recorded host contract does not emit a provider status on errors.
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

type hostError struct {
	kind  error
	cause error
}

func (e *hostError) Error() string        { return e.cause.Error() }
func (e *hostError) Unwrap() error        { return e.cause }
func (e *hostError) Is(target error) bool { return target == e.kind }

// OutcomeUnknown means the provider may have acted. Receipt retains the effect
// ID. It deliberately does not unwrap to a retryable Connect transport error;
// Cause exposes that diagnostic explicitly for logging and metrics only.
// Test the outcome with errors.Is(err, ErrOutcomeUnknown).
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
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return err
	}
	var info *errdetails.ErrorInfo
	var retry *errdetails.RetryInfo
	var bad *errdetails.BadRequest
	for _, detail := range ce.Details() {
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
		reset, parseErr := time.Parse(time.RFC3339Nano, info.GetMetadata()["reset_at"])
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
			return &OutcomeUnknown{cause: err}
		case "SOURCE_PROVIDER_REFUSED":
			return &ProviderRefused{cause: err}
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
