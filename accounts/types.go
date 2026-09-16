package accounts

import v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"

// The message types this package's methods take and return, re-exported under
// the facade's own name.
//
// A consumer must be able to call every method here while importing only this
// package. Naming `gen/...` in an exported signature would put that path into
// consumer source, which fixes where the stubs come from: the generated tree is
// this module's private implementation detail, and it moves — a regeneration
// against a newer contract, or one day a dependency on a registry-served
// package instead of a committed tree. A Go type alias makes these the *same*
// types, so this costs nothing at runtime and breaks no existing caller that
// already names the generated package.
type (
	// QueryAuditLogRequest is the request for AuditClient.QueryAuditLog.
	QueryAuditLogRequest = v1.QueryAuditLogRequest
	// QueryAuditLogResponse is the response from AuditClient.QueryAuditLog.
	QueryAuditLogResponse = v1.QueryAuditLogResponse
	// AuditEvent is one entry of a QueryAuditLogResponse.
	AuditEvent = v1.AuditEvent
)
