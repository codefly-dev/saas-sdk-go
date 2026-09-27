package moduleauthority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	codefly "github.com/codefly-dev/sdk-go/workcontext"
)

// The host gateway's module broker (module-saas-starter,
// module/services/auth-gateway/code/gateway_modules.go). These paths, headers
// and JSON shapes are its wire contract with composed modules.
const (
	workContextPath            = "/modules/_work-context"
	operationContextPath       = "/modules/_operation-context"
	sourceOperationContextPath = "/modules/_source-operation-context"

	// internalTokenHeader carries the host's internal perimeter credential, to
	// the broker and to the authority endpoint alike.
	internalTokenHeader = "X-Codefly-Internal-Token"
	// moduleSecretHeader carries the module's identity secret to the broker,
	// which forwards it only on its internal leg to accounts.
	moduleSecretHeader = "X-Codefly-Module-Secret"
)

// maxBrokerResponse bounds what this client reads from the broker. Its
// answers are a handful of short strings.
const maxBrokerResponse = 64 << 10

// BrokerError is a non-success answer from the gateway's module broker: the
// HTTP status and the broker's plain-text reason. Every broker failure this
// package returns wraps one, alongside a sentinel where the status has a
// meaning (401 ErrInvalidCredentials; 403 ErrPermissionDenied or a delegation
// refusal; 412 ErrDelegationMissing). 400 is a request the broker would not
// accept; 502 means the broker could not reach accounts and is worth retrying.
type BrokerError struct {
	Path       string
	StatusCode int
	Reason     string
}

func (e *BrokerError) Error() string {
	return fmt.Sprintf("module broker %s: HTTP %d: %s", e.Path, e.StatusCode, e.Reason)
}

type broker struct {
	baseURL string
	client  *http.Client
	token   string
}

func newBroker(seams Seams) (broker, error) {
	if seams.Gateway == nil {
		return broker{}, fmt.Errorf("%w: the gateway is required", ErrInvalidSeams)
	}
	base, err := gatewayBaseURL(seams.Gateway.BaseURL(), seams.AllowInsecureHTTP)
	if err != nil {
		return broker{}, err
	}
	httpClient := seams.Gateway.HTTPClient()
	if httpClient == nil {
		return broker{}, fmt.Errorf("%w: the gateway HTTP client is required", ErrInvalidSeams)
	}
	// Never follow a redirect: net/http would replay these custom credential
	// headers to wherever it points, which the transport rule never judged.
	noRedirect := *httpClient
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return broker{baseURL: base, client: &noRedirect, token: seams.InternalToken}, nil
}

// post sends one broker request and decodes a 200 answer into out. Any other
// status is a *BrokerError.
func (b broker) post(ctx context.Context, path, secret string, body map[string]string, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(internalTokenHeader, b.token)
	req.Header.Set(moduleSecretHeader, secret)
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBrokerResponse+1))
	if err != nil {
		return err
	}
	if len(raw) > maxBrokerResponse {
		return fmt.Errorf("module broker %s: response exceeds %d bytes", path, maxBrokerResponse)
	}
	if resp.StatusCode != http.StatusOK {
		return &BrokerError{Path: path, StatusCode: resp.StatusCode, Reason: strings.TrimSpace(string(raw))}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: undecodable broker answer", ErrInvalidCapability)
	}
	return nil
}

// brokerRefusal types the statuses the mints share: 401 is an unproven
// module, 403 a proven one refused. Anything else stays a bare *BrokerError.
func brokerRefusal(err error) error {
	var be *BrokerError
	if !errors.As(err, &be) {
		return err
	}
	switch be.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %w", ErrInvalidCredentials, err)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %w", ErrPermissionDenied, err)
	}
	return err
}

// workContextResponse is POST /modules/_work-context's answer. Its field
// names are camelCase, unlike the operation mints'.
type workContextResponse struct {
	Token       string `json:"token"`
	ExpiresAt   string `json:"expiresAt"`
	PrincipalID string `json:"principalId"`
	Tenant      string `json:"tenant"`
}

// operationContextResponse is POST /modules/_operation-context's answer, and
// the common part of /modules/_source-operation-context's.
type operationContextResponse struct {
	WorkContext string `json:"work_context"`
	ExpiresAt   string `json:"expires_at"`
	PrincipalID string `json:"principal_id"`
	Tenant      string `json:"tenant"`
	Audience    string `json:"audience"`
	Binding     string `json:"binding"`
}

func (r operationContextResponse) operationContext(now time.Time) (OperationContext, error) {
	expiresAt, err := parseExpiry(r.ExpiresAt, now)
	if err != nil {
		return OperationContext{}, err
	}
	token, err := codefly.ParseWorkContextToken(r.WorkContext)
	if err != nil {
		return OperationContext{}, fmt.Errorf("%w: malformed operation token", ErrInvalidCapability)
	}
	return OperationContext{
		Token:       token,
		ExpiresAt:   expiresAt,
		PrincipalID: r.PrincipalID,
		Tenant:      r.Tenant,
		Audience:    r.Audience,
		BindingID:   r.Binding,
	}, nil
}

// parseExpiry reads the broker's RFC 3339 expiry and refuses one already
// past.
func parseExpiry(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("%w: missing expiry", ErrInvalidCapability)
	}
	expiresAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: malformed expiry", ErrInvalidCapability)
	}
	if !expiresAt.After(now) {
		return time.Time{}, fmt.Errorf("%w: capability is already expired", ErrInvalidCapability)
	}
	return expiresAt, nil
}
