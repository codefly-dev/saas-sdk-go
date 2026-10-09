package datasource

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func detailedError(t *testing.T, code connect.Code, details ...proto.Message) *connect.Error {
	t.Helper()
	err := connect.NewError(code, errors.New("provider said credential-looking-example-secret"))
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
			if tt.want == ErrOutcomeUnknown {
				var retryable *connect.Error
				if errors.As(got, &retryable) {
					t.Fatal("unknown outcome exposes a retryable transport error")
				}
			} else if !errors.Is(got, tt.err) {
				t.Fatal("host error was lost")
			}
		})
	}

	t.Run("input pointer", func(t *testing.T) {
		err := detailedError(t, connect.CodeInvalidArgument, &errdetails.BadRequest{
			FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "/invoice/items/0/id"}},
		})
		var input *InputError
		if !errors.As(mapHostError(err, true, now), &input) || input.Pointer != "/invoice/items/0/id" || input.Error() != err.Error() {
			t.Fatalf("input = %#v", input)
		}
	})
	t.Run("provider status", func(t *testing.T) {
		err := detailedError(t, connect.CodeFailedPrecondition, &errdetails.ErrorInfo{
			Reason: "DATASOURCE_PROVIDER_REFUSED", Metadata: map[string]string{"provider_status": "403"},
		})
		var refused *ProviderRefused
		if !errors.As(mapHostError(err, true, now), &refused) || refused.Status != 403 || refused.Error() != err.Error() {
			t.Fatalf("refusal = %#v", refused)
		}
	})
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
	plain := errors.New("local error")
	if mapHostError(plain, true, now) != plain || mapHostError(nil, true, now) != nil {
		t.Fatal("non-Connect error changed")
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
