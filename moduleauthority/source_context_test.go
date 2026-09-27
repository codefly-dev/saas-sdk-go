package moduleauthority

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestMintSourceOperationContextNamesOnlyTheSource(t *testing.T) {
	h := newHost(t)
	h.gateway.expires = time.Now().Add(time.Minute)
	client := h.client(t, moduleCredentials)

	got, err := client.MintSourceOperationContext(t.Context(), exampleSource)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tenant != testTenant || got.OwnerPrincipalID != testOwner || got.DelegationID != testDelegation ||
		got.SourceID != exampleSource || got.Audience != "runtime" || got.BindingID != "runtime-admission" ||
		got.PrincipalID != testPrincipal || got.Token.Encoded() != "operation.token" {
		t.Fatalf("context: %+v", got)
	}
	if _, err := client.MintSourceOperationContext(t.Context(), exampleSource); err != nil {
		t.Fatal(err)
	}
	calls, _, bodies, headers := h.gateway.snapshot()
	if len(calls) != 2 {
		t.Fatalf("a source context is minted per call, never cached: %v", calls)
	}
	body := bodies[hostSourceOperationContextPath][0]
	if len(body) != 2 || body["prefix"] != testPrefix || body["source_id"] != exampleSource {
		t.Fatalf("body = %v, want exactly prefix and source_id", body)
	}
	if headers[0].Get("X-Codefly-Module-Secret") != testSecret || headers[0].Get("X-Codefly-Internal-Token") != testInternalToken {
		t.Fatalf("headers = %v", headers[0])
	}
	h.assertNoStrayPaths(t)
}

func TestMintSourceOperationContextTypesEveryDelegationRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		refuse refusal
		want   error
	}{
		{"missing", refusal{http.StatusPreconditionFailed, "DELEGATION_MISSING"}, ErrDelegationMissing},
		{"revoked", refusal{http.StatusForbidden, "DELEGATION_REVOKED"}, ErrDelegationRevoked},
		{"invalid", refusal{http.StatusForbidden, "DELEGATION_INVALID"}, ErrDelegationInvalid},
		// A host that names no reason in its 403 (the body before the
		// reasons landed): every 403 of this mint is a delegation the module
		// may not use.
		{"403 without a reason", refusal{http.StatusForbidden, "forbidden"}, ErrDelegationInvalid},
		{"unproven module", refusal{http.StatusUnauthorized, "unauthorized"}, ErrInvalidCredentials},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHost(t)
			h.gateway.refuse[hostSourceOperationContextPath] = tc.refuse
			_, err := h.client(t, moduleCredentials).MintSourceOperationContext(t.Context(), exampleSource)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			var be *BrokerError
			if !errors.As(err, &be) || be.StatusCode != tc.refuse.status {
				t.Fatalf("the refusal does not wrap the broker status: %v", err)
			}
		})
	}
}

func TestMintSourceOperationContextClaimsNoDelegationRefusalItWasNotGiven(t *testing.T) {
	// An unproven module or an outage is no reason to tell a person to
	// reconnect a source.
	for _, rf := range []refusal{
		{http.StatusUnauthorized, "unauthorized"},
		{http.StatusBadGateway, "source operation context unavailable"},
		{http.StatusBadRequest, "exactly one of delegation_id or source_id is required"},
		{http.StatusPreconditionFailed, "SOMETHING_ELSE"},
	} {
		h := newHost(t)
		h.gateway.refuse[hostSourceOperationContextPath] = rf
		_, err := h.client(t, moduleCredentials).MintSourceOperationContext(t.Context(), exampleSource)
		if err == nil || errors.Is(err, ErrDelegationMissing) || errors.Is(err, ErrDelegationRevoked) || errors.Is(err, ErrDelegationInvalid) {
			t.Fatalf("HTTP %d %q was typed as a delegation refusal: %v", rf.status, rf.body, err)
		}
	}
}

func TestMintSourceOperationContextRefusesAnAnswerForAnotherSourceOrExpired(t *testing.T) {
	h := newHost(t)
	h.gateway.sourceID = "66666666-6666-4666-8666-666666666666"
	if _, err := h.client(t, moduleCredentials).MintSourceOperationContext(t.Context(), exampleSource); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("a context for another source: %v", err)
	}
	h = newHost(t)
	h.gateway.expires = time.Now().Add(-time.Minute)
	if _, err := h.client(t, moduleCredentials).MintSourceOperationContext(t.Context(), exampleSource); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("an expired context: %v", err)
	}
}
