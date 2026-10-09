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
	// ErrOutcomeUnknown means the effect may have happened. Inspect the receipt
	// with Lookup; never interpret this outcome as permission to repeat it.
	ErrOutcomeUnknown = errors.New("datasource: outcome unknown; look up the effect")
)

// InputError reports a rejected input. Pointer is the host's JSON Pointer from
// BadRequest.FieldViolation.Field, or empty for a local JSON encoding failure.
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

// ProviderRefused reports a provider refusal. Status is the provider's HTTP
// status, or zero when the host supplies none. The original message is retained.
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

type hostError struct {
	kind  error
	cause error
}

func (e *hostError) Error() string        { return e.cause.Error() }
func (e *hostError) Unwrap() error        { return e.cause }
func (e *hostError) Is(target error) bool { return target == e.kind }

// An unknown outcome deliberately does not unwrap to a retryable Connect
// Unavailable/DeadlineExceeded error. Its diagnostic message remains unchanged.
type unknownOutcome struct{ cause error }

func (e *unknownOutcome) Error() string        { return e.cause.Error() }
func (e *unknownOutcome) Is(target error) bool { return target == ErrOutcomeUnknown }

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
		status, parseErr := strconv.Atoi(info.GetMetadata()["provider_status"])
		if parseErr != nil || status < 100 || status > 599 {
			status = 0
		}
		return &ProviderRefused{Status: status, cause: err}
	case connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeCanceled:
		if effectOutcome {
			return &unknownOutcome{cause: err}
		}
	}
	return err
}
