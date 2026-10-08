package moduleauthority

import (
	"context"
	"errors"
	"fmt"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
	"google.golang.org/protobuf/types/known/structpb"
)

var ErrInvalidAuditEvent = errors.New("module authority: incomplete or invalid audit event")

// AuditEvent is the complete intent presented to the host audit spine. Tenant
// may be empty only for an authorized system event. Actor and solution scope are
// checked by the host; naming them is not itself proof of authority.
type AuditEvent struct {
	Tenant, EventType, Actor, Solution, EntryID string
	Fields                                      map[string]any
	// Retain the same key and complete intent after an uncertain acknowledgement.
	// Lookup requires a key; an unkeyed emission cannot be recovered this way.
	IdempotencyKey string
}

// AuditReceipt identifies a committed event for the requested intent. An empty
// EventID is inconclusive, not proof that no effect happened or permission to
// repeat it. The host rechecks current emission authority on every lookup.
type AuditReceipt struct{ EventID string }

func auditRequest(event AuditEvent, lookup bool) (*v1.ModuleEmitAuditEventRequest, error) {
	if event.EventType == "" || event.Actor == "" || event.Solution == "" || (lookup && event.IdempotencyKey == "") {
		return nil, ErrInvalidAuditEvent
	}
	fields, err := structpb.NewStruct(event.Fields)
	if err != nil {
		return nil, fmt.Errorf("%w: unsupported field value", ErrInvalidAuditEvent)
	}
	return &v1.ModuleEmitAuditEventRequest{Tenant: event.Tenant, EventType: event.EventType,
		Actor: event.Actor, Solution: event.Solution, EntryId: event.EntryID,
		Fields: fields, IdempotencyKey: event.IdempotencyKey}, nil
}

// EmitAuditEvent commits a registered event as this module. Errors retain their
// host codes, including FailedPrecondition for conflicting or unverifiable keys.
// Only rejected authentication is refreshed once; effect failures are not retried.
func (c *Client) EmitAuditEvent(ctx context.Context, event AuditEvent) error {
	request, err := auditRequest(event, false)
	if err != nil {
		return err
	}
	_, err = callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.EmitAuditEvent, request)
	if err != nil {
		return fmt.Errorf("module authority: emit audit event: %w", err)
	}
	return nil
}

// LookupAuditEvent is read-only and requires the complete original event intent.
// It neither emits an event nor turns a missing receipt into a retry decision.
func (c *Client) LookupAuditEvent(ctx context.Context, event AuditEvent) (AuditReceipt, error) {
	request, err := auditRequest(event, true)
	if err != nil {
		return AuditReceipt{}, err
	}
	response, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.LookupAuditEvent, request)
	if err != nil {
		return AuditReceipt{}, fmt.Errorf("module authority: lookup audit event: %w", err)
	}
	return AuditReceipt{EventID: response.GetEventId()}, nil
}
