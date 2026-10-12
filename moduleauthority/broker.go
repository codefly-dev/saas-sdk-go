package moduleauthority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
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
	// bearerScheme carries the module pod's projected ServiceAccount token to
	// the broker as the request's bearer; the broker forwards it unread to
	// accounts, which reviews it and binds it to the build the pod is running.
	// The X-Codefly-Module-Secret header it replaced is gone with the secret.
	bearerScheme = "Bearer "
)

// maxBrokerResponse bounds what this client reads from the broker. Its
// answers are a handful of short strings.
const maxBrokerResponse = 64 << 10

// REST request bodies match gateway_modules.go at module-saas-starter v0.0.94
// (lines 185–188, 273–277 and 427–431). Credentials travel only in headers.
type workContextRequest struct {
	Prefix         string `json:"prefix"`
	OrganizationID string `json:"organization_id,omitempty"`
}

type operationContextRequest struct {
	Prefix         string `json:"prefix"`
	Binding        string `json:"binding"`
	OrganizationID string `json:"organization_id,omitempty"`
}

type sourceOperationContextRequest struct {
	// This facade selects the gateway's source_id arm; it never sends the
	// mutually exclusive delegation_id or an organization selector.
	Prefix   string `json:"prefix"`
	SourceID string `json:"source_id"`
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
// status is a *BrokerError, classified without reading its body. Transport
// and response read failures are *BrokerTransportError, without a cause.
func (b broker) post(ctx context.Context, path, projectedToken string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return brokerTransportError(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(internalTokenHeader, b.token)
	req.Header.Set("Authorization", bearerScheme+projectedToken)
	resp, err := b.client.Do(req)
	if err != nil {
		return brokerTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Exclude diagnostics entirely: a body, header or parser error can
		// reflect any credential in forms a sanitizer cannot recognize.
		return newBrokerError(resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBrokerResponse+1))
	if err != nil {
		return brokerTransportError(err)
	}
	if len(raw) > maxBrokerResponse {
		return fmt.Errorf("module broker %s: response exceeds %d bytes", path, maxBrokerResponse)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: undecodable broker answer", ErrInvalidCapability)
	}
	return nil
}

// brokerRefusal types the statuses the mints share: 401 is an unproven
// module, 403 a proven one refused, 503 a host that could not establish what
// this pod is running. Anything else stays a bare *BrokerError.
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
	case http.StatusServiceUnavailable:
		return fmt.Errorf("%w: %w", ErrExecutionUnestablished, err)
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
	if !sealed(r.WorkContext) {
		return OperationContext{}, fmt.Errorf("%w: malformed operation token", ErrInvalidCapability)
	}
	return OperationContext{
		Token:       r.WorkContext,
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
