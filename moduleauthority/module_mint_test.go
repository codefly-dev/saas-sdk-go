package moduleauthority

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMintModuleWorkContextDoesNotShareTheCache(t *testing.T) {
	h := newHost(t)
	now := time.Now().UTC().Truncate(time.Second)
	h.gateway.expires = now.Add(5 * time.Minute) // The sealed fixture lives an hour.
	client := h.client(t, moduleCredentials)
	client.now = func() time.Time { return now }

	for _, name := range []string{"module.token1", "module.token2"} {
		issued, err := client.MintModuleWorkContext(t.Context())
		if err != nil || issued.Token != sealedToken(name) || !issued.ExpiresAt.Equal(h.gateway.expires) {
			t.Fatalf("uncached mint %s: unexpected token/expiry or error %v", name, err)
		}
	}
	cached, err := client.ModuleWorkContext(t.Context())
	if err != nil || cached != sealedToken("module.token3") {
		t.Fatalf("uncached mint populated cache: %v", err)
	}
	issued, err := client.MintModuleWorkContext(t.Context())
	if err != nil || issued.Token != sealedToken("module.token4") {
		t.Fatalf("uncached mint reused cache: %v", err)
	}
	if got, err := client.ModuleWorkContext(t.Context()); err != nil || got != cached {
		t.Fatalf("uncached mint replaced cache: %v", err)
	}
	// Refresh follows the broker's shorter expiry, not the opaque token's claims.
	now = h.gateway.expires.Add(-20 * time.Second)
	if got, err := client.ModuleWorkContext(t.Context()); err != nil || got != sealedToken("module.token5") {
		t.Fatalf("cached mint did not refresh at broker expiry: %v", err)
	}
	if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 5 {
		t.Fatalf("broker calls = %v, want five mints", calls)
	}
}

func TestModuleWorkContextConcurrentCallsShareMint(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	type result struct {
		token string
		err   error
	}
	results := make(chan result, 8)
	start := make(chan struct{})
	for range cap(results) {
		go func() {
			<-start
			token, err := client.ModuleWorkContext(t.Context())
			results <- result{token, err}
		}()
	}
	close(start)
	for range cap(results) {
		r := <-results
		if r.err != nil || r.token != sealedToken("module.token1") {
			t.Errorf("concurrent cached mint: unexpected token or error %v", r.err)
		}
	}
	if calls, _, _, _ := h.gateway.snapshot(); len(calls) != 1 {
		t.Fatalf("broker calls = %v, want one shared mint", calls)
	}
}

func TestMintModuleWorkContextKeepsClientOrganization(t *testing.T) {
	h := newHost(t)
	organizations := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	for _, org := range organizations {
		client := h.client(t, Credentials{Prefix: testPrefix, Token: staticToken(testProjectedToken), OrganizationID: org})
		if _, err := client.MintModuleWorkContext(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	calls, _, bodies, headers := h.gateway.snapshot()
	if len(calls) != len(organizations) {
		t.Fatalf("broker calls = %v", calls)
	}
	for i, org := range organizations {
		body := bodies[hostWorkContextPath][i]
		if len(body) != 2 || body["prefix"] != testPrefix || body["organization_id"] != org {
			t.Fatalf("client %d sent unexpected body: %v", i, body)
		}
		assertProjectedBearer(t, headers[i], testProjectedToken)
	}
	h.assertNoStrayPaths(t)
}

func TestMintModuleWorkContextReturnsExactBrokerExpiry(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(2*time.Minute + 123456789*time.Nanosecond).In(time.FixedZone("broker", 2*60*60))
	token := sealedToken("broker-expiry")
	raw, err := json.Marshal(workContextResponse{Token: token, ExpiresAt: expires.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	client := brokerErrorClient(t, 200, testProjectedToken, func(*http.Request) io.ReadCloser {
		return io.NopCloser(strings.NewReader(string(raw)))
	})
	client.now = func() time.Time { return now }
	issued, err := client.MintModuleWorkContext(t.Context())
	if err != nil || issued.Token != token || !issued.ExpiresAt.Equal(expires) {
		t.Fatalf("mint did not retain exact broker token/expiry: %v", err)
	}
}

func TestMintModuleWorkContextRejectsInvalidAnswers(t *testing.T) {
	now := time.Now().UTC()
	token := sealedToken("invalid-answer")
	for _, tc := range []struct {
		name, token, expiry string
	}{
		{"missing expiry", token, ""},
		{"malformed expiry", token, "not-a-time"},
		{"expired", token, now.Add(-time.Second).Format(time.RFC3339Nano)},
		{"expires now", token, now.Format(time.RFC3339Nano)},
		{"missing token", "", now.Add(time.Minute).Format(time.RFC3339Nano)},
		{"unsealed token", "opaque.but-not-core", now.Add(time.Minute).Format(time.RFC3339Nano)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(workContextResponse{Token: tc.token, ExpiresAt: tc.expiry})
			if err != nil {
				t.Fatal(err)
			}
			client := brokerErrorClient(t, 200, testProjectedToken, func(*http.Request) io.ReadCloser {
				return io.NopCloser(strings.NewReader(string(raw)))
			})
			client.now = func() time.Time { return now }
			if issued, err := client.MintModuleWorkContext(t.Context()); !errors.Is(err, ErrInvalidCapability) || issued != (ModuleContext{}) {
				t.Fatalf("invalid answer returned a capability or wrong error: %v", err)
			}
		})
	}
}

func TestMintModuleWorkContextBoundsResponseRead(t *testing.T) {
	body := &brokerDiagnosticBody{Reader: strings.NewReader(strings.Repeat(" ", 2*maxBrokerResponse))}
	client := brokerErrorClient(t, 200, testProjectedToken, func(*http.Request) io.ReadCloser { return body })
	issued, err := client.MintModuleWorkContext(t.Context())
	if err == nil || issued != (ModuleContext{}) || !body.closed || body.read != maxBrokerResponse+1 {
		t.Fatalf("oversized answer: err=%v, closed=%v, read=%d", err, body.closed, body.read)
	}
}

func TestWorkloadBrokerMintsForItsOrganizationWithoutPerimeterCredential(t *testing.T) {
	const org = "11111111-1111-4111-8111-111111111111"
	other := "22222222-2222-4222-8222-222222222222"
	for _, tenant := range []string{org, other, ""} {
		gateway := brokerErrorGateway{client: &http.Client{Transport: brokerErrorTransport(func(r *http.Request) (*http.Response, error) {
			var in workContextRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Fatal(err)
			}
			if r.URL.Path != hostWorkContextPath || in.OrganizationID != org || in.Prefix != testPrefix ||
				r.Header.Get("Authorization") != "Bearer "+testProjectedToken || r.Header.Values(internalTokenHeader) != nil {
				t.Fatal("unexpected workload broker request")
			}
			raw, err := json.Marshal(workContextResponse{Token: sealedToken("journal"), Tenant: tenant, ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)})
			if err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})}}
		c, err := NewWorkloadBroker(gateway, Credentials{Prefix: testPrefix, Token: staticToken(testProjectedToken), OrganizationID: org}, false)
		if err != nil {
			t.Fatal(err)
		}
		token, err := c.ModuleWorkContext(t.Context())
		if tenant == org {
			if err != nil || token != sealedToken("journal") {
				t.Fatalf("expected selected tenant capability: %v", err)
			}
		} else if !errors.Is(err, ErrInvalidCapability) || token != "" {
			t.Fatal("accepted capability for an unconfirmed tenant")
		}
	}
}
