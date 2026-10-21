package moduleauthority

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

type brokerReadFailure struct{ err error }

func (b brokerReadFailure) Read(p []byte) (int, error) {
	return copy(p, "partial response: "+testProjectedToken), b.err
}

func TestBrokerTransportFailuresExcludeCauses(t *testing.T) {
	leak := "Bearer " + testProjectedToken + "; X-Codefly-Internal-Token: " + testInternalToken
	for _, tc := range []struct {
		name     string
		cause    error
		category BrokerTransportCategory
	}{
		{"deadline", fmt.Errorf("%s: %w", leak, context.DeadlineExceeded), BrokerTransportTimeout},
		{"timeout", &net.DNSError{Name: leak, Err: leak, IsTimeout: true}, BrokerTransportTimeout},
		{"wrapped timeout", fmt.Errorf("%s: %w", leak, &net.DNSError{Name: leak, Err: leak, IsTimeout: true}), BrokerTransportTimeout},
		{"joined timeout", errors.Join(&net.DNSError{Name: leak}, &net.DNSError{Name: leak, IsTimeout: true}), BrokerTransportTimeout},
		{"connection refused", &net.OpError{Op: leak, Net: leak, Err: &os.SyscallError{Syscall: leak, Err: syscall.ECONNREFUSED}}, BrokerTransportConnectionRefused},
		{"connection reset", &net.OpError{Op: leak, Net: leak, Err: &os.SyscallError{Syscall: leak, Err: syscall.ECONNRESET}}, BrokerTransportConnectionReset},
		{"wrapped reset", fmt.Errorf("%s: %w", leak, syscall.ECONNRESET), BrokerTransportConnectionReset},
		{"joined reset", errors.Join(errors.New(leak), syscall.ECONNRESET), BrokerTransportConnectionReset},
		{"reset and deadline", errors.Join(syscall.ECONNRESET, context.DeadlineExceeded), BrokerTransportTimeout},
		{"TLS verification", &tls.CertificateVerificationError{Err: errors.New(leak)}, BrokerTransportTLS},
		{"TLS record", tls.RecordHeaderError{Msg: leak}, BrokerTransportTLS},
		{"TLS alert", fmt.Errorf("%s: %w", leak, tls.AlertError(42)), BrokerTransportTLS},
		{"x509 authority", fmt.Errorf("%s: %w", leak, x509.UnknownAuthorityError{Cert: &x509.Certificate{}}), BrokerTransportTLS},
		{"x509 hostname", x509.HostnameError{Certificate: &x509.Certificate{}, Host: leak}, BrokerTransportTLS},
		{"x509 certificate", x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.IncompatibleUsage, Detail: leak}, BrokerTransportTLS},
		{"x509 roots", x509.SystemRootsError{Err: errors.New(leak)}, BrokerTransportTLS},
		{"canceled", fmt.Errorf("%s: %w", leak, context.Canceled), BrokerTransportOther},
		{"parser", errors.New("malformed MIME header: " + leak), BrokerTransportOther},
		{"text is not a category", errors.New("TLS timeout connection refused reset: " + leak), BrokerTransportOther},
	} {
		for _, mint := range brokerMints(t) {
			for _, phase := range []string{"transport", "read"} {
				t.Run(fmt.Sprintf("%s/%s/%s", tc.name, mint.path, phase), func(t *testing.T) {
					// Even a URL, operation name and wrapped cause may contain a
					// reflected credential. Only error identities/types survive.
					underlying := &url.Error{Op: leak, URL: leak, Err: tc.cause}
					calls := 0
					client := brokerTransportClient(t, testProjectedToken, brokerErrorTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						if phase == "transport" {
							return nil, underlying
						}
						return &http.Response{StatusCode: 200, Header: r.Header.Clone(), Body: io.NopCloser(brokerReadFailure{underlying}), Request: r}, nil
					}))
					err := mint.call(client)
					assertBrokerTransportError(t, err, tc.category)
					assertBrokerErrorExcludes(t, err, leak, testProjectedToken, testInternalToken, "partial response")
					if calls != 1 {
						t.Errorf("made %d requests, want one without an automatic retry", calls)
					}
					var exposed *url.Error
					if errors.As(err, &exposed) || errors.Is(err, tc.cause) || errors.Is(err, underlying) {
						t.Error("transport error exposes the underlying cause")
					}
				})
			}
		}
	}
}

// Panicking Error methods prove classification never inspects error text.
type brokerUnprintableError struct{ cause error }

func (brokerUnprintableError) Error() string   { panic("must not inspect underlying Error text") }
func (e brokerUnprintableError) Unwrap() error { return e.cause }

type brokerUnprintableTimeout struct{ brokerUnprintableError }

func (brokerUnprintableTimeout) Timeout() bool   { return true }
func (brokerUnprintableTimeout) Temporary() bool { return true }

func TestBrokerTransportClassificationNeverFormatsCause(t *testing.T) {
	for _, tc := range []struct {
		cause    error
		category BrokerTransportCategory
	}{
		{brokerUnprintableError{context.DeadlineExceeded}, BrokerTransportTimeout},
		{brokerUnprintableTimeout{}, BrokerTransportTimeout},
		{brokerUnprintableError{syscall.ECONNREFUSED}, BrokerTransportConnectionRefused},
		{brokerUnprintableError{syscall.ECONNRESET}, BrokerTransportConnectionReset},
		{brokerUnprintableError{tls.RecordHeaderError{}}, BrokerTransportTLS},
		{brokerUnprintableError{errors.New("timeout TLS connection refused")}, BrokerTransportOther},
	} {
		assertBrokerTransportError(t, brokerTransportError(tc.cause), tc.category)
	}
}

func assertBrokerTransportError(t *testing.T, err error, category BrokerTransportCategory) {
	t.Helper()
	var te *BrokerTransportError
	if !errors.As(err, &te) || te.Category != category {
		t.Fatalf("lost transport category %d: %v", category, err)
	}
	want := map[BrokerTransportCategory]string{
		BrokerTransportOther:             "module broker: transport or response read failed",
		BrokerTransportTimeout:           "module broker: transport or response read timed out",
		BrokerTransportConnectionRefused: "module broker: connection refused",
		BrokerTransportTLS:               "module broker: TLS failure",
		BrokerTransportConnectionReset:   "module broker: connection reset",
	}[category]
	if te.Error() != want || fmt.Sprintf("%+v", te) != want {
		t.Error("transport diagnostic is not the fixed category message")
	}
	assertBrokerErrorFields(t, te, map[string]reflect.Type{"Category": reflect.TypeOf(BrokerTransportOther)})
	assertNoBrokerCause(t, te)
	var be *BrokerError
	var netErr net.Error
	if errors.As(err, &be) || errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, syscall.ECONNRESET) {
		t.Error("transport failure exposes a status or an underlying cause")
	}
}

func TestBrokerParserErrorsExcludeCredentials(t *testing.T) {
	for _, secret := range []string{testProjectedToken, testInternalToken} {
		for _, tc := range []struct {
			name   string
			wire   string
			status int // Zero means no usable non-200 response was obtained.
		}{
			{"initial header", "HTTP/1.1 401 Unauthorized\r\nBearer " + secret + "\r\n\r\n", 0},
			{"success trailer", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nBearer " + secret + "\r\n\r\n", 0},
			{"refused trailer", "HTTP/1.1 401 Unauthorized\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nBearer " + secret + "\r\n\r\n", 401},
			{"forbidden trailer", "HTTP/1.1 403 Forbidden\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nBearer " + secret + "\r\n\r\n", 403},
			{"unestablished trailer", "HTTP/1.1 503 Unavailable\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nBearer " + secret + "\r\n\r\n", 503},
		} {
			t.Run(tc.name+"/"+secret, func(t *testing.T) {
				// Confirm the real net/http parser exposes this fixture's secret
				// without our boundary; all bytes travel over an in-memory pipe.
				control := &http.Client{Transport: brokerParserTransport(t, tc.wire)}
				resp, rawErr := control.Get("https://broker.invalid")
				if rawErr == nil {
					_, rawErr = io.ReadAll(resp.Body)
					_ = resp.Body.Close()
				}
				if rawErr == nil || !strings.Contains(rawErr.Error(), secret) {
					t.Fatal("parser fixture did not produce the credential-bearing error")
				}
				for _, mint := range brokerMints(t) {
					client := brokerTransportClient(t, testProjectedToken, brokerParserTransport(t, tc.wire))
					err := mint.call(client)
					if tc.status == 0 {
						assertBrokerTransportError(t, err, BrokerTransportOther)
					} else {
						assertBrokerError(t, err, tc.status)
					}
					assertBrokerErrorExcludes(t, err, secret, "Bearer", "malformed MIME header")
				}
			})
		}
	}
}

func brokerParserTransport(t *testing.T, wire string) *http.Transport {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = server.Close() }()
		req, err := http.ReadRequest(bufio.NewReader(server))
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		_, _ = io.WriteString(server, wire)
	}()
	transport := &http.Transport{
		// DialTLSContext returns the pipe directly: exercise HTTP framing
		// without a TLS handshake, listener, DNS lookup or network access.
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		transport.CloseIdleConnections()
		<-done
	})
	return transport
}

func TestBrokerCallersCanDecideRetries(t *testing.T) {
	// A caller can make a conservative retry decision with errors.As alone;
	// it never needs a body diagnostic or the underlying transport error.
	retry := func(err error) bool {
		var be *BrokerError
		if errors.As(err, &be) {
			return be.Classification == BrokerErrorNotEstablished || be.StatusCode == 502
		}
		var te *BrokerTransportError
		return errors.As(err, &te) && (te.Category == BrokerTransportTimeout || te.Category == BrokerTransportConnectionRefused || te.Category == BrokerTransportConnectionReset)
	}
	for _, mint := range brokerMints(t) {
		for _, status := range []int{401, 403, 503, 502, 400, 412} {
			client := brokerErrorClient(t, status, testProjectedToken, func(*http.Request) io.ReadCloser {
				return io.NopCloser(brokenBrokerBody{})
			})
			if got, want := retry(mint.call(client)), status == 503 || status == 502; got != want {
				t.Errorf("%s HTTP %d: retry = %v, want %v", mint.path, status, got, want)
			}
		}
		for _, tc := range []struct {
			cause error
			retry bool
		}{
			{context.DeadlineExceeded, true},
			{syscall.ECONNREFUSED, true},
			{syscall.ECONNRESET, true},
			{tls.RecordHeaderError{}, false},
			{errors.New("unknown parser failure"), false},
			{context.Canceled, false},
		} {
			client := brokerTransportClient(t, testProjectedToken, brokerErrorTransport(func(*http.Request) (*http.Response, error) {
				return nil, tc.cause
			}))
			if got := retry(mint.call(client)); got != tc.retry {
				t.Errorf("%s transport: retry = %v, want %v", mint.path, got, tc.retry)
			}
		}
	}
}
