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
//
// Re-exporting the types named in a signature is not sufficient on its own:
// every type reachable *through* them has to be nameable here too, and an
// enum needs its constants re-exported because an alias does not carry them.
// internal/apiboundary enforces both.
type (
	// QueryAuditLogRequest is the request for AuditClient.QueryAuditLog.
	QueryAuditLogRequest = v1.QueryAuditLogRequest
	// QueryAuditLogResponse is the response from AuditClient.QueryAuditLog.
	QueryAuditLogResponse = v1.QueryAuditLogResponse
	// AuditEvent is one entry of a QueryAuditLogResponse.
	AuditEvent = v1.AuditEvent

	// Organization is one organization the caller belongs to.
	Organization = v1.Organization
	// ListOrganizationsRequest is the (empty) request behind
	// OrganizationsClient.ListOrganizations.
	ListOrganizationsRequest = v1.ListOrganizationsRequest

	// ListMyAccessibleScopesRequest is the request for
	// AccessibleScopesClient.ListMyAccessibleScopes.
	ListMyAccessibleScopesRequest = v1.ListMyAccessibleScopesRequest
	// ListAccessibleScopesResponse is one page of accessible scopes.
	ListAccessibleScopesResponse = v1.ListAccessibleScopesResponse
	// AccessibleScope is one scope node the caller may act on.
	AccessibleScope = v1.AccessibleScope

	// StartTaskWorkContextRequest is the request for WorkContextsClient.StartTask.
	StartTaskWorkContextRequest = v1.StartTaskWorkContextRequest
	// WorkContextScope is one slice of authority a Work Context carries.
	WorkContextScope = v1.WorkContextScope
	// WorkContextReplayPolicy selects whether an issued context may be replayed.
	WorkContextReplayPolicy = v1.WorkContextReplayPolicy
	// IssuedWorkContext is the signed context accounts issued, with the
	// principals it resolved.
	IssuedWorkContext = v1.IssuedWorkContext
)

// WorkContextReplayPolicy values, re-exported because an alias does not carry
// the constants declared with the type.
const (
	WorkContextReplayPolicyUnspecified = v1.WorkContextReplayPolicy_WORK_CONTEXT_REPLAY_POLICY_UNSPECIFIED
	WorkContextReplayPolicyIdempotent  = v1.WorkContextReplayPolicy_WORK_CONTEXT_REPLAY_POLICY_IDEMPOTENT
	WorkContextReplayPolicySingleUse   = v1.WorkContextReplayPolicy_WORK_CONTEXT_REPLAY_POLICY_SINGLE_USE
)
