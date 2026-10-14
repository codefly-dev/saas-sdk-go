package moduleauthority

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// brokerMintCase is one mint and the sentinel each mint types a status with.
// The statuses are driven by a raw transport, not by the host fake: they pin
// the SDK's mapping for any status, including one the gateway never sends on
// that path (a 403 on the work-context mint — accounts' PermissionDenied is a
// 401 there). The real gateway's statuses per path are held by
// conformance/hostgateway, which is where the 412 below came from: the gateway
// now relays accounts' FailedPrecondition on the work-context and
// operation-context mints as 412 rather than 502.
type brokerMintCase struct {
	path               string
	call               func(*Client) error
	forbidden          error
	preconditionFailed error
}

func brokerMints(t *testing.T) []brokerMintCase {
	return []brokerMintCase{
		{hostWorkContextPath, func(c *Client) error { _, err := c.ModuleWorkContext(t.Context()); return err }, ErrPermissionDenied, ErrNoHostStateToMintFor},
		{hostWorkContextPath, func(c *Client) error { _, err := c.MintModuleWorkContext(t.Context()); return err }, ErrPermissionDenied, ErrNoHostStateToMintFor},
		{hostOperationContextPath, func(c *Client) error {
			_, err := c.MintModuleOperationContext(t.Context(), "binding")
			return err
		}, ErrPermissionDenied, ErrNoHostStateToMintFor},
		{hostSourceOperationContextPath, func(c *Client) error {
			_, err := c.MintSourceOperationContext(t.Context(), exampleSource)
			return err
		}, ErrDelegationInvalid, ErrDelegationMissing},
	}
}

// Exercise the public mint methods without a listener or network access.
type brokerErrorTransport func(*http.Request) (*http.Response, error)

func (f brokerErrorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type brokerErrorGateway struct{ client *http.Client }

func (g brokerErrorGateway) BaseURL() string          { return "https://broker.invalid" }
func (g brokerErrorGateway) HTTPClient() *http.Client { return g.client }

func brokerErrorClient(t *testing.T, status int, projected string, body func(*http.Request) io.ReadCloser) *Client {
	t.Helper()
	return brokerTransportClient(t, projected, brokerErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+projected {
			t.Fatal("broker request did not carry the projected bearer")
		}
		if r.Header.Get(internalTokenHeader) != testInternalToken {
			t.Fatal("broker request did not carry the internal token")
		}
		// Reflect both credentials in headers as well as the supplied body.
		return &http.Response{StatusCode: status, Header: r.Header.Clone(), Body: body(r), Request: r}, nil
	}))
}

func brokerTransportClient(t *testing.T, projected string, transport http.RoundTripper) *Client {
	t.Helper()
	httpClient := &http.Client{Transport: transport}
	client, err := New(Seams{
		Gateway:       brokerErrorGateway{client: httpClient},
		InternalToken: testInternalToken,
	}, Credentials{Prefix: testPrefix, Token: staticToken(projected)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

type brokerDiagnosticBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *brokerDiagnosticBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *brokerDiagnosticBody) Close() error { b.closed = true; return nil }

type brokenBrokerBody struct{ partial string }

func (b brokenBrokerBody) Read(p []byte) (int, error) {
	return copy(p, b.partial), io.ErrUnexpectedEOF
}

func TestBrokerFailuresPreserveStatus(t *testing.T) {
	for _, mint := range brokerMints(t) {
		for _, status := range []int{401, 403, 503, 400, 502, 307, 412, 201, 204} {
			for _, failure := range []string{"oversized", "unreadable", "partial"} {
				t.Run(fmt.Sprintf("%s/%d/%s", mint.path, status, failure), func(t *testing.T) {
					var reader io.Reader
					switch failure {
					case "oversized":
						// A bearer straddles the read limit: retaining a partial
						// diagnostic would retain part of the credential.
						reader = strings.NewReader(strings.Repeat(" ", (64<<10)-5) + testProjectedToken + strings.Repeat("x", 100))
					case "unreadable":
						reader = brokenBrokerBody{}
					case "partial":
						reader = brokenBrokerBody{partial: "Authorization: Bearer " + testProjectedToken[:5]}
					}
					body := &brokerDiagnosticBody{Reader: reader}
					client := brokerErrorClient(t, status, testProjectedToken, func(*http.Request) io.ReadCloser { return body })
					err := mint.call(client)
					assertBrokerError(t, err, status)
					var want error
					switch status {
					case 401:
						want = ErrInvalidCredentials
					case 403:
						want = mint.forbidden
					case 412:
						want = mint.preconditionFailed
					case 503:
						want = ErrExecutionUnestablished
					}
					for _, sentinel := range []error{ErrInvalidCredentials, ErrPermissionDenied, ErrDelegationInvalid, ErrDelegationMissing, ErrDelegationRevoked, ErrExecutionUnestablished, ErrNoHostStateToMintFor} {
						if errors.Is(err, sentinel) != (sentinel == want) {
							t.Errorf("classification = %v, want only %v", err, want)
						}
					}
					if !body.closed || body.read != 0 {
						t.Errorf("body closed = %v, bytes read = %d", body.closed, body.read)
					}
				})
			}
		}
	}
}

func TestBrokerFailureExcludesBearer(t *testing.T) {
	for _, projected := range []string{"synthetic-projected-bearer", "first.second.third", "prefix/first.second.third/suffix"} {
		for _, status := range []int{401, 403, 503, 502} {
			t.Run(fmt.Sprintf("%s/%d", projected, status), func(t *testing.T) {
				client := brokerErrorClient(t, status, projected, func(r *http.Request) io.ReadCloser {
					return io.NopCloser(strings.NewReader("<html>" + r.Header.Get("Authorization") + "; echoed=" + projected + "</html>"))
				})
				_, err := client.ModuleWorkContext(t.Context())
				assertBrokerError(t, err, status)
				assertBrokerErrorExcludes(t, err, projected, testInternalToken, "<html>", "echoed=")
			})
		}
	}
}

func TestBrokerFailureExcludesJWT(t *testing.T) {
	for _, jwt := range []string{"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMifQ.c2lnbmF0dXJl", "a.b.c", "a_-0.b_1-.C-2_"} {
		t.Run(jwt, func(t *testing.T) {
			client := brokerErrorClient(t, 403, testProjectedToken, func(*http.Request) io.ReadCloser {
				return io.NopCloser(strings.NewReader("unrelated credential: (" + jwt + "), again=" + jwt))
			})
			_, err := client.ModuleWorkContext(t.Context())
			assertBrokerError(t, err, 403)
			assertBrokerErrorExcludes(t, err, jwt, "unrelated credential", "again=")
		})
	}
}

func TestBrokerFailureExcludesResponseDiagnostics(t *testing.T) {
	const projected = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMifQ.c2lnbmF0dXJl"
	encoded := strings.ReplaceAll(projected, ".", "%2E")
	for _, tc := range []struct {
		name string
		body string
	}{
		{"projected", "Authorization: Bearer " + projected},
		{"internal", "X-Codefly-Internal-Token: " + testInternalToken},
		{"URL encoded", encoded},
		{"base64", base64.StdEncoding.EncodeToString([]byte(projected))},
		{"split", strings.ReplaceAll(projected, ".", ".\n")},
		{"partial clean EOF", "Bearer " + projected[:20]},
		{"opaque partial clean EOF", "Bearer synthetic-opaque-secret"},
		{"overlapping JWT", "auth.header." + projected},
		{"encoded at limit", strings.Repeat("!", 240) + encoded},
		// Preserve the old sanitizer's control and truncation regressions,
		// now requiring complete exclusion instead of partial retention.
		{"controls", "\x00denied\r\n\t\x1b\x7f\u202e\u00e9\xffend\x00"},
		{"length", strings.Repeat("x", 1024)},
		{"bearer at limit", strings.Repeat("x", 240) + testProjectedToken},
		{"JWT at limit", strings.Repeat("!", 240) + "first.second.third"},
		{"revoked reason", "DELEGATION_REVOKED"},
		{"missing reason", "DELEGATION_MISSING"},
	} {
		for _, mint := range brokerMints(t) {
			for _, status := range []int{401, 403, 503, 502, 412} {
				t.Run(fmt.Sprintf("%s/%s/%d", tc.name, mint.path, status), func(t *testing.T) {
					client := brokerErrorClient(t, status, projected, func(*http.Request) io.ReadCloser {
						return io.NopCloser(strings.NewReader(tc.body))
					})
					err := mint.call(client)
					assertBrokerError(t, err, status)
					assertBrokerErrorExcludes(t, err, tc.body, projected, testInternalToken, projected[:20])
				})
			}
		}
	}
}

// Exact messages and the field allowlist catch even a fragment we did not
// anticipate, rather than depending on a blacklist of credential encodings.
func assertBrokerError(t *testing.T, err error, status int) {
	t.Helper()
	var be *BrokerError
	if !errors.As(err, &be) || be.StatusCode != status {
		t.Fatalf("lost broker status %d: %v", status, err)
	}
	classification, message := BrokerErrorOther, "request failed"
	switch status {
	case 401, 403:
		classification, message = BrokerErrorRefused, "request refused"
	case 503:
		classification, message = BrokerErrorNotEstablished, "execution not established"
	}
	if be.Classification != classification {
		t.Errorf("classification = %v, want %v", be.Classification, classification)
	}
	want := fmt.Sprintf("module broker: HTTP %d: %s", status, message)
	if be.Error() != want || fmt.Sprintf("%+v", be) != want {
		t.Error("broker diagnostic is not the fixed status/class message")
	}
	assertBrokerErrorFields(t, be, map[string]reflect.Type{
		"StatusCode": reflect.TypeOf(int(0)), "Classification": reflect.TypeOf(BrokerErrorOther),
	})
	var transport *BrokerTransportError
	if errors.As(err, &transport) || errors.Is(err, io.ErrUnexpectedEOF) {
		t.Error("non-200 status became a transport/read failure")
	}
	assertNoBrokerCause(t, be)
}

func assertBrokerErrorFields(t *testing.T, err error, fields map[string]reflect.Type) {
	t.Helper()
	typ := reflect.TypeOf(err).Elem()
	if typ.NumField() != len(fields) {
		t.Fatalf("%s retains fields beyond its safe classification", typ)
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if fields[field.Name] != field.Type || !field.IsExported() {
			t.Errorf("%s retains unexpected field %s of type %s", typ, field.Name, field.Type)
		}
	}
}

func assertNoBrokerCause(t *testing.T, err error) {
	t.Helper()
	if _, ok := err.(interface{ Unwrap() error }); ok {
		t.Error("broker error exposes an Unwrap method")
	}
	if _, ok := err.(interface{ Unwrap() []error }); ok {
		t.Error("broker error exposes multiple causes")
	}
}

func assertBrokerErrorExcludes(t *testing.T, err error, excluded ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a broker failure")
	}
	texts := []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)}
	value := reflect.ValueOf(err)
	if value.Kind() == reflect.Pointer && value.Elem().Kind() == reflect.Struct {
		value = value.Elem()
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).IsExported() {
				texts = append(texts, fmt.Sprintf("%+v", value.Field(i).Interface()))
			}
		}
	}
	for _, text := range texts {
		for _, secret := range excluded {
			if secret != "" && strings.Contains(text, secret) {
				t.Error("error text, formatting or an exported field contains an excluded diagnostic")
			}
		}
	}
	switch e := err.(type) {
	case interface{ Unwrap() error }:
		if cause := e.Unwrap(); cause != nil {
			assertBrokerErrorExcludes(t, cause, excluded...)
		}
	case interface{ Unwrap() []error }:
		for _, cause := range e.Unwrap() {
			assertBrokerErrorExcludes(t, cause, excluded...)
		}
	}
}
