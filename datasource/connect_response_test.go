package datasource

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type observedBody struct {
	io.Reader
	reads, closes int
}

func (b *observedBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (b *observedBody) Close() error               { b.closes++; return nil }

func TestUnsupportedEncodingClosesResponseWithoutReading(t *testing.T) {
	body := &observedBody{Reader: strings.NewReader(`{"code":"permission_denied"}`)}
	httpClient := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden, Request: req, Body: body,
			Header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"br"}},
		}, nil
	})}
	req, err := http.NewRequest(http.MethodPost, "https://example.com/invoke", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/proto")
	response, err := (connectResponseClient{client: httpClient}).Do(req)
	var unsupported *unsupportedResponseEncoding
	if response != nil || !errors.As(err, &unsupported) || unsupported.encoding != "br" ||
		connect.CodeOf(err) != connect.CodeInternal || body.reads != 0 || body.closes != 1 {
		t.Fatalf("unsupported response = %v, %v, reads=%d closes=%d", response, err, body.reads, body.closes)
	}
}
