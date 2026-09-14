// Package authorizationrevision checks already verified Work Context claims
// against Accounts' current authorization revision. It does not verify signatures,
// choose permissions, cache decisions, or grant authority.
package authorizationrevision

import (
	"context"
	"errors"
	"time"

	base "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	gen "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const Timeout = 3 * time.Second
const InternalCredentialHeader = "x-codefly-internal-token"

// These errors intentionally contain no server message, metadata or credential.
var (
	ErrDenied      = errors.New("current authorization was not confirmed")
	ErrUnavailable = errors.New("current authorization service unavailable")
)

// CredentialProvider returns the current service credential for each check. It
// must honor cancellation; it must not return a caller bearer or Work Context.
type CredentialProvider func(context.Context) (string, error)

// RPC is the canonical Accounts unary method, not a domain authorization port.
type RPC interface {
	CheckAuthorizationRevision(context.Context, *gen.CheckAuthorizationRevisionRequest, ...grpc.CallOption) (*emptypb.Empty, error)
}

type grpcClient struct{ connection grpc.ClientConnInterface }

// NewGRPCClient adapts a composition-owned connection. Its TLS trust, target and
// interceptors remain the connection owner's responsibility. The wrapper neither
// dials nor follows an application-supplied endpoint. Use grpc.WithDisableRetry
// when creating that connection; Check also disables per-call retry buffering.
func NewGRPCClient(connection grpc.ClientConnInterface) RPC {
	return grpcClient{connection: connection}
}

func (c grpcClient) CheckAuthorizationRevision(ctx context.Context, request *gen.CheckAuthorizationRevisionRequest, options ...grpc.CallOption) (*emptypb.Empty, error) {
	if c.connection == nil {
		return nil, ErrUnavailable
	}
	response := new(emptypb.Empty)
	if err := c.connection.Invoke(ctx, accountsv1connect.WorkContextServiceCheckAuthorizationRevisionProcedure, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

// Project copies the owner and every actor's original scopes without reducing
// them to the final actor's effective scope. The caller has already verified the
// claims and retains action/resource, lifetime and lineage policy.
func Project(claims *base.WorkContextV1) (*gen.CheckAuthorizationRevisionRequest, error) {
	if claims == nil || claims.GetTenantId() == "" || claims.GetOwnerPrincipalId() == "" {
		return nil, ErrUnavailable
	}
	request := &gen.CheckAuthorizationRevisionRequest{OrgId: claims.GetTenantId(), OwnerPrincipalId: claims.GetOwnerPrincipalId(), AuthorizationRevision: claims.GetAuthorizationRevision()}
	add := func(principal string, scopes []*base.WorkScopeV1) error {
		if principal == "" {
			return ErrUnavailable
		}
		subject := &gen.WorkContextRevisionSubject{PrincipalId: principal}
		for _, scope := range scopes {
			if scope == nil {
				return ErrUnavailable
			}
			subject.Scopes = append(subject.Scopes, &gen.WorkContextScope{ResourceKind: scope.GetResourceKind(), Actions: append([]string(nil), scope.GetActions()...), ResourceIds: append([]string(nil), scope.GetResourceIds()...)})
		}
		request.Subjects = append(request.Subjects, subject)
		return nil
	}
	if err := add(claims.GetOwnerPrincipalId(), claims.GetAuthorityScopes()); err != nil {
		return nil, err
	}
	for _, actor := range claims.GetActorChain() {
		if actor == nil {
			return nil, ErrUnavailable
		}
		if err := add(actor.GetPrincipalId(), actor.GetGrantedScopes()); err != nil {
			return nil, err
		}
	}
	return request, nil
}

// Check performs one current query under a total three-second bound, or the
// caller's earlier deadline. It replaces outgoing metadata rather than forwarding
// user credentials. RPC and CredentialProvider implementations must honor ctx.
// PermissionDenied/FailedPrecondition mean current denial; internal credential,
// transport and response-contract failures mean unavailable. Callers decide how
// those classes map onto their own public API.
func Check(ctx context.Context, rpc RPC, credential CredentialProvider, claims *base.WorkContextV1) error {
	if rpc == nil || credential == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(ctx, metadata.MD{}), Timeout)
	defer cancel()
	request, err := Project(claims)
	if err != nil {
		return ErrUnavailable
	}
	token, err := credential(ctx)
	if err != nil || len(token) == 0 || len(token) > 8192 || ctx.Err() != nil {
		return ErrUnavailable
	}
	for _, c := range []byte(token) {
		if c < 33 || c > 126 {
			return ErrUnavailable
		}
	}
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(InternalCredentialHeader, token))
	response, err := rpc.CheckAuthorizationRevision(ctx, request, grpc.WaitForReady(false), grpc.MaxRetryRPCBufferSize(0), grpc.MaxCallRecvMsgSize(8192), grpc.MaxCallSendMsgSize(65536))
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	if err != nil {
		switch status.Code(err) {
		case codes.PermissionDenied, codes.FailedPrecondition:
			return ErrDenied
		default:
			return ErrUnavailable
		}
	}
	if response == nil || len(response.ProtoReflect().GetUnknown()) != 0 {
		return ErrUnavailable
	}
	return nil
}
