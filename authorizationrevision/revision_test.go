package authorizationrevision

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	base "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	gen "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func claims() *base.WorkContextV1 {
	return &base.WorkContextV1{TenantId: "tenant", OwnerPrincipalId: "owner", AuthorizationRevision: 7, AuthorityScopes: []*base.WorkScopeV1{{ResourceKind: "example.items", Actions: []string{"read"}}}}
}

func TestSharedProjection(t *testing.T) {
	data, err := os.ReadFile("testdata/projection.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Claims, Request json.RawMessage }
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	var input base.WorkContextV1
	var want gen.CheckAuthorizationRevisionRequest
	if err = protojson.Unmarshal(fixture.Claims, &input); err != nil {
		t.Fatal(err)
	}
	if err = protojson.Unmarshal(fixture.Request, &want); err != nil {
		t.Fatal(err)
	}
	got, err := Project(&input)
	if err != nil || !proto.Equal(got, &want) {
		t.Fatalf("projection: %v %v", got, err)
	}
	got.Subjects[0].Scopes[0].Actions[0] = "changed"
	got.Subjects[1].Scopes[0].ResourceIds[0] = "changed"
	if input.AuthorityScopes[0].Actions[0] != "read" || input.ActorChain[0].GrantedScopes[0].ResourceIds[0] != "item-1" {
		t.Fatal("projection aliases verified claims")
	}
}

type rpcFunc func(context.Context, *gen.CheckAuthorizationRevisionRequest, ...grpc.CallOption) (*emptypb.Empty, error)

func (f rpcFunc) CheckAuthorizationRevision(c context.Context, r *gen.CheckAuthorizationRevisionRequest, o ...grpc.CallOption) (*emptypb.Empty, error) {
	return f(c, r, o...)
}

func TestTypedFailuresAndExactEmptyResponse(t *testing.T) {
	unknown := &emptypb.Empty{}
	unknown.ProtoReflect().SetUnknown([]byte{8, 1})
	for _, tc := range []struct {
		name         string
		reply        *emptypb.Empty
		rpcErr, want error
	}{
		{"ok", &emptypb.Empty{}, nil, nil}, {"nil", nil, nil, ErrUnavailable}, {"unknown", unknown, nil, ErrUnavailable},
		{"denial", nil, status.Error(codes.PermissionDenied, "private detail"), ErrDenied},
		{"revision", nil, status.Error(codes.FailedPrecondition, "private detail"), ErrDenied},
		{"credential", nil, status.Error(codes.Unauthenticated, "private detail"), ErrUnavailable},
		{"outage", nil, status.Error(codes.Unavailable, "private detail"), ErrUnavailable},
		{"invalid reply", nil, status.Error(codes.Internal, "private detail"), ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			rpc := rpcFunc(func(context.Context, *gen.CheckAuthorizationRevisionRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
				calls++
				return tc.reply, tc.rpcErr
			})
			err := Check(context.Background(), rpc, func(context.Context) (string, error) { return "internal-test", nil }, claims())
			if !errors.Is(err, tc.want) || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("server detail escaped")
			}
		})
	}
}

func TestCredentialCancellationAndIsolation(t *testing.T) {
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "user-secret", "cookie", "user-cookie"))
	ctx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	calls := 0
	rpc := rpcFunc(func(context.Context, *gen.CheckAuthorizationRevisionRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
		calls++
		return &emptypb.Empty{}, nil
	})
	provider := func(ctx context.Context) (string, error) {
		md, _ := metadata.FromOutgoingContext(ctx)
		if len(md) != 0 {
			t.Fatal("caller metadata reached credential provider")
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	if err := Check(ctx, rpc, provider, claims()); !errors.Is(err, ErrUnavailable) || calls != 0 {
		t.Fatalf("%v calls=%d", err, calls)
	}
	for _, token := range []string{"", "contains space", "line\nbreak", strings.Repeat("x", 8193)} {
		if err := Check(context.Background(), rpc, func(context.Context) (string, error) { return token, nil }, claims()); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid credential dispatched")
	}
}

type oracle interface {
	CheckAuthorizationRevision(context.Context, *gen.CheckAuthorizationRevisionRequest) (*emptypb.Empty, error)
}
type server struct {
	mu       sync.Mutex
	calls    int
	token    string
	revision uint64
	denied   bool
	requests []*gen.CheckAuthorizationRevisionRequest
}

func (s *server) CheckAuthorizationRevision(ctx context.Context, r *gen.CheckAuthorizationRevisionRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.requests = append(s.requests, r)
	md, _ := metadata.FromIncomingContext(ctx)
	if !reflect.DeepEqual(md.Get(InternalCredentialHeader), []string{s.token}) || len(md.Get("authorization")) != 0 || len(md.Get("cookie")) != 0 || len(md.Get("x-codefly-work-context")) != 0 {
		return nil, status.Error(codes.Unauthenticated, "internal-only credential required")
	}
	if s.denied || r.AuthorizationRevision != s.revision {
		return nil, status.Error(codes.FailedPrecondition, "revoked")
	}
	return &emptypb.Empty{}, nil
}
func serve(t *testing.T, s oracle) RPC {
	t.Helper()
	l := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	g.RegisterService(&grpc.ServiceDesc{ServiceName: "saas.accounts.v1.WorkContextService", HandlerType: (*oracle)(nil), Methods: []grpc.MethodDesc{{MethodName: "CheckAuthorizationRevision", Handler: func(srv any, ctx context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		r := new(gen.CheckAuthorizationRevisionRequest)
		if err := decode(r); err != nil {
			return nil, err
		}
		return srv.(oracle).CheckAuthorizationRevision(ctx, r)
	}}}}, s)
	go g.Serve(l)
	t.Cleanup(g.Stop)
	c, err := grpc.NewClient("passthrough:///accounts", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return l.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return NewGRPCClient(c)
}
func TestGRPCCurrentRevisionCredentialAndActorRevocation(t *testing.T) {
	s := &server{token: "internal-one", revision: 7}
	rpc := serve(t, s)
	token := "internal-one"
	credentials := 0
	provider := func(context.Context) (string, error) { credentials++; return token, nil }
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "caller", "cookie", "caller", "x-codefly-work-context", "caller"))
	c := claims()
	if err := Check(ctx, rpc, provider, c); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.token = "internal-two"
	s.revision = 8
	s.mu.Unlock()
	token = "internal-two"
	if err := Check(ctx, rpc, provider, c); !errors.Is(err, ErrDenied) {
		t.Fatal("stale revision not observed", err)
	}
	c.AuthorizationRevision = 8
	if err := Check(ctx, rpc, provider, c); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.denied = true
	s.mu.Unlock()
	if err := Check(ctx, rpc, provider, c); !errors.Is(err, ErrDenied) {
		t.Fatal("revocation not observed", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls != 4 || credentials != 4 {
		t.Fatalf("calls=%d credentials=%d", s.calls, credentials)
	}
}

func TestTotalBudgetIncludesCredentialAndRPC(t *testing.T) {
	var deadline time.Time
	provider := func(ctx context.Context) (string, error) {
		var ok bool
		deadline, ok = ctx.Deadline()
		if !ok || time.Until(deadline) > Timeout {
			t.Fatal("missing total bound")
		}
		time.Sleep(20 * time.Millisecond)
		return "internal-test", nil
	}
	calls := 0
	rpc := rpcFunc(func(ctx context.Context, _ *gen.CheckAuthorizationRevisionRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
		calls++
		actual, _ := ctx.Deadline()
		if !actual.Equal(deadline) {
			t.Fatal("RPC restarted credential deadline")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Check(ctx, rpc, provider, claims()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if calls != 1 || time.Since(start) > time.Second {
		t.Fatal("caller bound not preserved")
	}
}

func TestMalformedProjectionNeverDispatches(t *testing.T) {
	badActor := claims()
	badActor.ActorChain = []*base.WorkActorV1{nil}
	badScope := claims()
	badScope.AuthorityScopes = []*base.WorkScopeV1{nil}
	for _, input := range []*base.WorkContextV1{nil, {}, badActor, badScope} {
		rpc := rpcFunc(func(context.Context, *gen.CheckAuthorizationRevisionRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
			t.Fatal("invalid claims dispatched")
			return nil, nil
		})
		if err := Check(context.Background(), rpc, func(context.Context) (string, error) { t.Fatal("invalid claims requested credential"); return "", nil }, input); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
}
