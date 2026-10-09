package datasource

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func detailedError(t *testing.T, code connect.Code, details ...proto.Message) *connect.Error {
	t.Helper()
	err := connect.NewWireError(code, errors.New("provider said credential-looking-example-secret"))
	for _, message := range details {
		detail, detailErr := connect.NewErrorDetail(message)
		if detailErr != nil {
			t.Fatal(detailErr)
		}
		err.AddDetail(detail)
	}
	return err
}

func TestHostErrorMapping(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		err  *connect.Error
		want error
	}{
		{"permission", detailedError(t, connect.CodePermissionDenied), ErrNotPermitted},
		{"operation", detailedError(t, connect.CodeNotFound), ErrUnknownOperation},
		{"unavailable", detailedError(t, connect.CodeUnavailable), ErrOutcomeUnknown},
		{"deadline", detailedError(t, connect.CodeDeadlineExceeded), ErrOutcomeUnknown},
		{"canceled", detailedError(t, connect.CodeCanceled), ErrOutcomeUnknown},
		{"lost mutation", detailedError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{Reason: "SOURCE_OPERATION_OUTCOME_UNKNOWN"}), ErrOutcomeUnknown},
		{"effect reuse", detailedError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{Reason: "SOURCE_EFFECT_REUSED"}), ErrEffectReused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapHostError(tt.err, true, now)
			if !errors.Is(got, tt.want) {
				t.Fatalf("error = %v, want %v", got, tt.want)
			}
			if got.Error() != tt.err.Error() {
				t.Fatalf("host diagnostic was changed: %q", got.Error())
			}
			var transport *connect.Error
			if !errors.Is(got, tt.err) || !errors.As(got, &transport) || transport != tt.err {
				t.Fatal("host error was lost")
			}
		})
	}

	t.Run("provider refusal without status", func(t *testing.T) {
		err := detailedError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{
			Reason: "SOURCE_PROVIDER_REFUSED",
		})
		var refused *ProviderRefused
		if !errors.As(mapHostError(err, true, now), &refused) || refused.Status != 0 || refused.Error() != err.Error() {
			t.Fatalf("refusal = %#v", refused)
		}
	})
	for _, tt := range []struct {
		metadata string
		status   int
	}{
		{"100", 100}, {"403", 403}, {"429", 429}, {"500", 500}, {"599", 599},
		{"", 0}, {"0", 0}, {"99", 0}, {"600", 0}, {"-403", 0}, {"+403", 0},
		{"0403", 0}, {" 403", 0}, {"403 ", 0}, {"403.0", 0}, {"４０３", 0},
		{"invalid", 0}, {"999999999999999999999999", 0},
	} {
		t.Run("provider status/"+tt.metadata, func(t *testing.T) {
			err := detailedError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{
				Reason: "SOURCE_PROVIDER_REFUSED", Domain: "saas.accounts.v1",
				Metadata: map[string]string{"provider_status": tt.metadata},
			})
			var refused *ProviderRefused
			if !errors.As(mapHostError(err, true, now), &refused) || refused.Status != tt.status ||
				refused.Error() != err.Error() || !errors.Is(refused, err) {
				t.Fatalf("provider refusal = %#v, want status %d with unchanged cause", refused, tt.status)
			}
		})
	}
	// Metadata never changes the meaning of the structured reason.
	unknown := detailedError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{
		Reason: "SOURCE_OPERATION_OUTCOME_UNKNOWN", Domain: "saas.accounts.v1",
		Metadata: map[string]string{"provider_status": "403"},
	})
	if mapped := mapHostError(unknown, true, now); !errors.Is(mapped, ErrOutcomeUnknown) {
		t.Fatalf("provider metadata changed an unknown outcome: %v", mapped)
	}
	for _, reason := range []string{"", "SOURCE_DECLARATION_CHANGED", "SOURCE_REAUTH_REQUIRED", "SOURCE_OUTPUT_REFUSED",
		"SOURCE_EFFECT_REQUIRED", "SOURCE_OPERATIONS_UNAVAILABLE", "SOURCE_DECLARATION_INVALID", "SOURCE_EFFECT_BINDING_CHANGED", "FUTURE_REASON"} {
		t.Run("precondition/"+reason, func(t *testing.T) {
			err := detailedError(t, connect.CodeFailedPrecondition)
			if reason != "" {
				err = detailedError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{Reason: reason})
			}
			got := mapHostError(err, true, now)
			var refused *OperationRefused
			var provider *ProviderRefused
			if !errors.As(got, &refused) || refused.Reason != reason || !errors.Is(got, err) || errors.As(got, &provider) {
				t.Fatalf("host precondition = %v", got)
			}
		})
	}
	t.Run("input without structured pointer", func(t *testing.T) {
		err := connect.NewWireError(connect.CodeInvalidArgument, errors.New("source operation: invalid at /invoice/id"))
		var invalid *InputError
		if !errors.As(mapHostError(err, true, now), &invalid) || invalid.Pointer != "" || invalid.Error() != err.Error() {
			t.Fatalf("input = %#v", invalid)
		}
	})
	for _, pointer := range []string{"/id", "/", "/items/0/a~1b~0c"} {
		t.Run("input pointer"+pointer, func(t *testing.T) {
			err := detailedError(t, connect.CodeInvalidArgument,
				&errdetails.ErrorInfo{Reason: "SOURCE_INPUT_REFUSED", Domain: "saas.accounts.v1"},
				&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{{
					Field: pointer, Description: "input does not satisfy the declaration",
				}}})
			var invalid *InputError
			if !errors.As(mapHostError(err, true, now), &invalid) || invalid.Pointer != pointer ||
				invalid.Error() != err.Error() || !errors.Is(invalid, err) {
				t.Fatalf("input = %#v, want pointer %q with unchanged cause", invalid, pointer)
			}
		})
	}
	for _, code := range []connect.Code{connect.CodeUnknown, connect.CodeUnauthenticated, connect.CodeAlreadyExists,
		connect.CodeAborted, connect.CodeOutOfRange, connect.CodeUnimplemented, connect.CodeInternal,
		connect.CodeDataLoss, connect.CodeResourceExhausted} {
		err := detailedError(t, code)
		if got := mapHostError(err, true, now); got != err {
			t.Errorf("unmapped %v changed to %v", code, got)
		}
	}
	for _, code := range []connect.Code{connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeCanceled} {
		err := detailedError(t, code)
		if got := mapHostError(err, false, now); got != err {
			t.Errorf("read transport failure %v was presented as an effect outcome: %v", err, got)
		}
	}
	plain := errors.New("transport error")
	if mapHostError(plain, false, now) != plain || mapHostError(nil, true, now) != nil {
		t.Fatal("unrelated error changed")
	}
	if mapped := mapHostError(plain, true, now); !errors.Is(mapped, ErrOutcomeUnknown) || !errors.Is(mapped, plain) {
		t.Fatal("post-dispatch transport error lost its unknown outcome or cause")
	}
}

func TestRateLimitReset(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name     string
		metadata map[string]string
		retry    *errdetails.RetryInfo
		want     time.Time
	}{
		{"retry delay", nil, &errdetails.RetryInfo{RetryDelay: durationpb.New(3 * time.Second)}, now.Add(3 * time.Second)},
		{"absolute wins", map[string]string{"reset_at": now.Add(time.Minute).Format(time.RFC3339)}, &errdetails.RetryInfo{RetryDelay: durationpb.New(time.Second)}, now.Add(time.Minute)},
		{"invalid absolute falls back", map[string]string{"reset_at": "invalid"}, &errdetails.RetryInfo{RetryDelay: durationpb.New(time.Second)}, now.Add(time.Second)},
		{"missing", nil, &errdetails.RetryInfo{}, time.Time{}},
		{"negative", nil, &errdetails.RetryInfo{RetryDelay: durationpb.New(-time.Second)}, time.Time{}},
		{"invalid duration", nil, &errdetails.RetryInfo{RetryDelay: &durationpb.Duration{Seconds: 1, Nanos: -1}}, time.Time{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := detailedError(t, connect.CodeResourceExhausted,
				&errdetails.ErrorInfo{Reason: "DATASOURCE_RATE_LIMITED", Metadata: tt.metadata}, tt.retry)
			var limited *RateLimited
			if !errors.As(mapHostError(err, true, now), &limited) || !limited.ResetAt.Equal(tt.want) {
				t.Fatalf("rate limit = %#v, want %v", limited, tt.want)
			}
			if limited.Error() != err.Error() || !errors.Is(limited, err) {
				t.Fatal("rate limit lost host diagnostic")
			}
		})
	}
}

func TestUnknownOutcomeWrapsContextAndConnectErrors(t *testing.T) {
	for _, tt := range []struct {
		code  connect.Code
		cause error
	}{
		{connect.CodeDeadlineExceeded, context.DeadlineExceeded},
		{connect.CodeCanceled, context.Canceled},
	} {
		host := connect.NewError(tt.code, tt.cause)
		mapped := mapHostError(host, true, time.Now())
		wrapped := fmt.Errorf("invoke: %w", mapped)
		var outcome *OutcomeUnknown
		var transport *connect.Error
		if !errors.Is(wrapped, ErrOutcomeUnknown) || !errors.Is(wrapped, tt.cause) ||
			!errors.As(wrapped, &outcome) || !errors.As(wrapped, &transport) || transport != host ||
			connect.CodeOf(wrapped) != tt.code || outcome.Unwrap() != host {
			t.Fatalf("unknown outcome lost its identity or cause: %v", wrapped)
		}
	}
}
