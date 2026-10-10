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
	// A durable relay must persist this key and complete event before sending,
	// and reuse them after a restart or uncertain acknowledgement. The host
	// deduplicates by (tenant, event type, key), not by comparing intent. Never
	// reuse a key for a different event. Empty disables deduplication.
	IdempotencyKey string
}

func auditRequest(event AuditEvent) (*v1.ModuleEmitAuditEventRequest, error) {
	if event.EventType == "" || event.Actor == "" || event.Solution == "" {
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

// EmitAuditEvent asks the host to commit a registered event as this module.
// A nil error acknowledges the host's durable write or a previously used key;
// it does not mean that downstream webhook delivery has completed. Host error
// codes are preserved. Only rejected authentication is refreshed once; other
// errors leave the outcome uncertain and are never automatically retried.
func (c *Client) EmitAuditEvent(ctx context.Context, event AuditEvent) error {
	request, err := auditRequest(event)
	if err != nil {
		return err
	}
	_, err = callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.EmitAuditEvent, request)
	if err != nil {
		return fmt.Errorf("module authority: emit audit event: %w", err)
	}
	return nil
}
