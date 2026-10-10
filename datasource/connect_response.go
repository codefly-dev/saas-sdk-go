package datasource

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"connectrpc.com/connect"
)

type unsupportedResponseEncoding struct{ encoding string }

func (e *unsupportedResponseEncoding) Error() string {
	return fmt.Sprintf("datasource: unsupported response encoding %q", e.encoding)
}

// connectResponseClient normalizes the shared SDK's accepted HTTP encodings
// before Connect decodes an error. It delegates the original request unchanged
// to the gateway client; Connect still owns envelope, detail and status decoding.
type connectResponseClient struct{ client *http.Client }

func (c connectResponseClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.client.Do(request)
	if err != nil ||
		strings.HasPrefix(request.Header.Get("Content-Type"), "application/grpc") {
		return response, err
	}
	encoding := strings.ToLower(response.Header.Get("Content-Encoding"))
	if encoding != "" && encoding != "identity" && encoding != "gzip" {
		// No body or HTTP status can establish an RPC outcome when its encoding
		// is unsupported. Close here because Connect receives an error, not a body.
		_ = response.Body.Close()
		return nil, connect.NewError(connect.CodeInternal, &unsupportedResponseEncoding{encoding: response.Header.Get("Content-Encoding")})
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	mediaType, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(params) > 1 ||
		(len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) {
		return response, nil
	}
	// Clone the response metadata, never the gateway's client/transport/request.
	normalized := *response
	normalized.Header = response.Header.Clone()
	normalized.Header.Set("Content-Type", "application/json")
	normalized.Header.Del("Content-Encoding")
	normalized.Header.Del("Content-Length")
	normalized.ContentLength = -1
	normalized.Uncompressed = response.Uncompressed || encoding == "gzip"
	normalized.Body = &connectJSONErrorBody{source: response.Body, compressed: encoding == "gzip"}
	return &normalized, nil
}

// Decode lazily so Connect retains responsibility for read failures and closing
// the original response. Peeking only the prefix avoids buffering another copy.
type connectJSONErrorBody struct {
	source      io.ReadCloser
	compressed  bool
	initialized bool
	reader      *bufio.Reader
	gzip        *gzip.Reader
	err         error
}

func (b *connectJSONErrorBody) Read(p []byte) (int, error) {
	if !b.initialized {
		b.initialized = true
		var source io.Reader = b.source
		if b.compressed {
			b.gzip, b.err = gzip.NewReader(b.source)
			if b.err != nil {
				return 0, b.err
			}
			source = b.gzip
		}
		b.reader = bufio.NewReader(source)
		prefix, _ := b.reader.Peek(3)
		if bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
			_, _ = b.reader.Discard(3)
		}
	}
	if b.err != nil {
		return 0, b.err
	}
	return b.reader.Read(p)
}

func (b *connectJSONErrorBody) Close() error {
	var err error
	if b.gzip != nil {
		err = b.gzip.Close()
	}
	return errors.Join(err, b.source.Close())
}
