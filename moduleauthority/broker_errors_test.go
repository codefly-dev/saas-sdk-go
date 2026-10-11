package moduleauthority

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Exercise the public mint methods without a listener or network access.
type brokerErrorTransport func(*http.Request) (*http.Response, error)

func (f brokerErrorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type brokerErrorGateway struct{ client *http.Client }

func (g brokerErrorGateway) BaseURL() string          { return "https://broker.invalid" }
func (g brokerErrorGateway) HTTPClient() *http.Client { return g.client }

func brokerErrorClient(t *testing.T, status int, projected string, body func(*http.Request) io.ReadCloser) *Client {
	t.Helper()
	httpClient := &http.Client{Transport: brokerErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+projected {
			t.Fatal("broker request did not carry the projected bearer")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: body(r), Request: r}, nil
	})}
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
	for _, mint := range []struct {
		path      string
		call      func(*Client) error
		forbidden error
	}{
		{hostWorkContextPath, func(c *Client) error { _, err := c.ModuleWorkContext(t.Context()); return err }, ErrPermissionDenied},
		{hostOperationContextPath, func(c *Client) error {
			_, err := c.MintModuleOperationContext(t.Context(), "binding")
			return err
		}, ErrPermissionDenied},
		{hostSourceOperationContextPath, func(c *Client) error {
			_, err := c.MintSourceOperationContext(t.Context(), exampleSource)
			return err
		}, ErrDelegationInvalid},
	} {
		for _, status := range []int{401, 403, 503, 400, 502, 307} {
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
					var be *BrokerError
					if !errors.As(err, &be) || be.StatusCode != status || be.Path != mint.path {
						t.Fatalf("lost broker status/path: %v", err)
					}
					var want error
					switch status {
					case 401:
						want = ErrInvalidCredentials
					case 403:
						want = mint.forbidden
					case 503:
						want = ErrExecutionUnestablished
					}
					for _, sentinel := range []error{ErrInvalidCredentials, ErrPermissionDenied, ErrDelegationInvalid, ErrExecutionUnestablished} {
						if errors.Is(err, sentinel) != (sentinel == want) {
							t.Errorf("classification = %v, want only %v", err, want)
						}
					}
					if be.Reason != "" {
						t.Error("retained an incomplete broker diagnostic")
					}
					if !body.closed || body.read > (64<<10)+1 {
						t.Errorf("body closed = %v, bytes read = %d", body.closed, body.read)
					}
				})
			}
		}
	}
}

func TestBrokerFailureRedactsBearer(t *testing.T) {
	for _, projected := range []string{"synthetic-projected-bearer", "first.second.third", "prefix/first.second.third/suffix"} {
		for _, status := range []int{401, 403, 503, 502} {
			t.Run(fmt.Sprintf("%s/%d", projected, status), func(t *testing.T) {
				client := brokerErrorClient(t, status, projected, func(r *http.Request) io.ReadCloser {
					return io.NopCloser(strings.NewReader("<html>" + r.Header.Get("Authorization") + "; echoed=" + projected + "</html>"))
				})
				_, err := client.ModuleWorkContext(t.Context())
				var be *BrokerError
				if !errors.As(err, &be) {
					t.Fatalf("missing broker error: %v", err)
				}
				if strings.Contains(be.Reason, projected) || strings.Contains(err.Error(), projected) {
					t.Error("reflected bearer leaked into Reason or Error()")
				}
				if want := "<html>Bearer [redacted]; echoed=[redacted]</html>"; be.Reason != want {
					t.Error("reflected bearer was not fully replaced with [redacted]")
				}
			})
		}
	}
}

func TestBrokerFailureRedactsJWT(t *testing.T) {
	for _, jwt := range []string{"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMifQ.c2lnbmF0dXJl", "a.b.c", "a_-0.b_1-.C-2_"} {
		t.Run(jwt, func(t *testing.T) {
			client := brokerErrorClient(t, 403, testProjectedToken, func(*http.Request) io.ReadCloser {
				return io.NopCloser(strings.NewReader("unrelated credential: (" + jwt + "), again=" + jwt))
			})
			_, err := client.ModuleWorkContext(t.Context())
			var be *BrokerError
			if !errors.As(err, &be) {
				t.Fatalf("missing broker error: %v", err)
			}
			if strings.Contains(be.Reason, jwt) || strings.Contains(err.Error(), jwt) {
				t.Error("JWT-shaped credential leaked into Reason or Error()")
			}
			if be.Reason != "unrelated credential: ([redacted]), again=[redacted]" {
				t.Error("JWT-shaped credentials were not replaced with [redacted]")
			}
		})
	}
}

func TestBrokerFailureReasonIsShortPrintableASCII(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"controls", "\x00denied\r\n\t\x1b\x7f\u202e\u00e9\xffend\x00", "denied        end"},
		{"length", strings.Repeat("x", 1024), strings.Repeat("x", 256)},
		// Redact before shortening, even when a credential crosses the limit.
		{"bearer at limit", strings.Repeat("x", 240) + testProjectedToken, strings.Repeat("x", 240) + "[redacted]"},
		{"JWT at limit", strings.Repeat("!", 240) + "first.second.third", strings.Repeat("!", 240) + "[redacted]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := brokerErrorClient(t, 401, testProjectedToken, func(*http.Request) io.ReadCloser {
				return io.NopCloser(strings.NewReader(tc.body))
			})
			_, err := client.ModuleWorkContext(t.Context())
			var be *BrokerError
			if !errors.As(err, &be) {
				t.Fatalf("missing broker error: %v", err)
			}
			if len(be.Reason) > 256 {
				t.Errorf("reason has %d bytes, want at most 256", len(be.Reason))
			}
			for _, text := range []string{be.Reason, err.Error()} {
				for _, ch := range text {
					if ch < ' ' || ch > '~' {
						t.Errorf("non-printable ASCII character %U in diagnostic", ch)
					}
				}
			}
			if be.Reason != tc.want {
				t.Error("reason was not sanitized and redacted before truncation")
			}
		})
	}
}
