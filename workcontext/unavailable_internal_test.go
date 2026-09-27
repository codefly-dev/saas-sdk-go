package workcontext

import (
	"fmt"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"
)

// A verification that could not run is Unavailable on every carrier, so a
// caller retries it instead of discarding a sound token as invalid.
func TestAnUnavailableVerificationIsUnavailableOnEveryCarrier(t *testing.T) {
	err := fmt.Errorf("%w: fetch Work Context JWKS: refused", ErrUnavailable)
	if got := HTTPStatus(err); got != http.StatusServiceUnavailable {
		t.Fatalf("HTTPStatus = %d, want 503", got)
	}
	if got := grpcCode(err); got != codes.Unavailable {
		t.Fatalf("grpcCode = %v, want Unavailable", got)
	}
	if got := connectCode(err); got != connect.CodeUnavailable {
		t.Fatalf("connectCode = %v, want Unavailable", got)
	}
}
