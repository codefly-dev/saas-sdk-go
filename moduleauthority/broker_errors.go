package moduleauthority

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
)

// BrokerErrorClass classifies a non-200 broker response by HTTP status alone.
type BrokerErrorClass uint8

const (
	BrokerErrorOther          BrokerErrorClass = iota // Any status except 200, 401, 403 and 503.
	BrokerErrorRefused                                // 401 or 403: the request was refused.
	BrokerErrorNotEstablished                         // 503: the host could not establish this pod's execution.
)

// BrokerError is a non-200 answer from the gateway's module broker. It retains
// only the HTTP status and its classification, and formats a fixed diagnostic
// per class. Response bodies, headers and underlying errors are never retained.
// Use errors.As to inspect it, including when wrapped with ErrInvalidCredentials
// (401), ErrPermissionDenied or ErrDelegationInvalid (403),
// ErrDelegationMissing (412 from the source mint), or
// ErrExecutionUnestablished (503). A 502 or 503 may be worth retrying; a refusal
// must not be treated as a transient transport failure.
type BrokerError struct {
	StatusCode     int
	Classification BrokerErrorClass
}

func (e *BrokerError) Error() string {
	message := "request failed"
	switch e.Classification {
	case BrokerErrorRefused:
		message = "request refused"
	case BrokerErrorNotEstablished:
		message = "execution not established"
	}
	return fmt.Sprintf("module broker: HTTP %d: %s", e.StatusCode, message)
}

func newBrokerError(status int) *BrokerError {
	classification := BrokerErrorOther
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		classification = BrokerErrorRefused
	case http.StatusServiceUnavailable:
		classification = BrokerErrorNotEstablished
	}
	return &BrokerError{StatusCode: status, Classification: classification}
}

// BrokerTransportCategory is a coarse classification of a transport or response
// read failure. It is derived from error types and identities, never error text.
type BrokerTransportCategory uint8

const (
	BrokerTransportOther             BrokerTransportCategory = iota // Includes cancellation and unrecognized parser errors.
	BrokerTransportTimeout                                          // Deadline exceeded or a net.Error reporting Timeout.
	BrokerTransportConnectionRefused                                // errors.Is(err, syscall.ECONNREFUSED).
	BrokerTransportTLS                                              // A recognized crypto/tls or crypto/x509 error type.
)

// BrokerTransportError reports a failure to obtain or read a broker response.
// Its message is fixed for its Category. It deliberately retains no underlying
// error and has no Unwrap method: even HTTP parser errors can contain credentials.
// Callers use errors.As and Category for retry decisions, and their context's
// Err method to check cancellation. An unusable HTTP response (including malformed
// initial headers) has no trustworthy status and is a transport failure; a
// successfully parsed non-200 status always produces BrokerError instead.
type BrokerTransportError struct {
	Category BrokerTransportCategory
}

func (e *BrokerTransportError) Error() string {
	switch e.Category {
	case BrokerTransportTimeout:
		return "module broker: transport or response read timed out"
	case BrokerTransportConnectionRefused:
		return "module broker: connection refused"
	case BrokerTransportTLS:
		return "module broker: TLS failure"
	default:
		return "module broker: transport or response read failed"
	}
}

func brokerTransportError(err error) *BrokerTransportError {
	category := BrokerTransportOther
	var verification *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	var roots x509.SystemRootsError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || brokerTimeout(err):
		category = BrokerTransportTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		category = BrokerTransportConnectionRefused
	case errors.As(err, &verification), errors.As(err, &record), errors.As(err, &alert),
		errors.As(err, &unknownAuthority), errors.As(err, &hostname),
		errors.As(err, &invalidCertificate), errors.As(err, &roots):
		category = BrokerTransportTLS
	}
	return &BrokerTransportError{Category: category}
}

func brokerTimeout(err error) bool {
	var netErr net.Error
	if !errors.As(err, &netErr) {
		return false
	}
	if netErr.Timeout() {
		return true
	}
	// A wrapper such as url.Error can report false while a deeper net.Error
	// reports a timeout (for example, across a fmt.Errorf wrapper).
	switch e := err.(type) {
	case interface{ Unwrap() error }:
		return brokerTimeout(e.Unwrap())
	case interface{ Unwrap() []error }:
		for _, cause := range e.Unwrap() {
			if brokerTimeout(cause) {
				return true
			}
		}
	}
	return false
}
