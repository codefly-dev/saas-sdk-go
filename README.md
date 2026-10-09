# saas-sdk-go

The **Go SDK for the saas accounts API** — a versioned, published client that
solutions depend on instead of regenerating and vendoring their own `gen/` tree.

The SDK surfaces are:

- **`gen/`** — the generated Connect + protobuf bindings for the accounts public
  proto (`saas.accounts.v1` and friends), generated from
  `codefly-dev/module-saas-starter` at the ref recorded below.
- **`accounts/`** — a thin, gateway-bound facade with syntactic sugar over the
  generated stubs. It hides the `connect.Request/Response` envelope and the raw
  procedure strings, so a solution handler writes plain protos:

  ```go
  resp, err := accounts.New(gw).Audit().QueryAuditLog(ctx, &v1.QueryAuditLogRequest{PageSize: 20})
  ```

  `gw` is any value exposing `BaseURL() string` and `HTTPClient() *http.Client` —
  which `github.com/codefly-dev/solution-runtime-go.Gateway` already satisfies.
  The runtime stays accounts-agnostic and takes **no** dependency on this SDK.
- **`datasource/`** — the same facade over `DatasourceService`, so a solution
  connects a GitHub datasource collection in a few lines:

  ```go
  ds := datasource.New(gw)
  src, err := ds.AddGitHubSource(ctx, datasource.GitHubSource{
      OrgID:       org,
      Repo:        "codefly-dev/module-saas-starter",
      Paths:       []string{"docs"},
      Collection:  "handbook",
      FileExtensions: []string{".md"},
      AccessToken: token,
  })
  _, err = ds.Sync(ctx, org, src.GetId())
  ```
- **`moduleauthority/`** — the module-principal side of installed operation
  authority. A long-running module supplies its Codefly-projected registration
  credential once; the client mints and refreshes the module's short-lived Work
  Context, attaches it to the generic SaaS exchange, and returns the exchanged
  child as an opaque `sdk-go` token. The signed-in person's retained parent and
  the exchanged child remain request-local.

  It talks to the two seams the host serves a composed module, and takes both
  explicitly — the consumer passes what Codefly resolved, nothing is defaulted:

  - the **gateway's module broker** (REST on the auth-gateway's `rest`
    endpoint): `POST /modules/_work-context`, `/modules/_operation-context`
    and `/modules/_source-operation-context`, where the module presents its
    identity secret. The host never serves the `Mint*` procedures of
    `ModuleCapabilitiesService` at the gateway edge;
  - accounts' **`authority` gRPC endpoint** (`saas/accounts/authority`), where
    the module calls, with its module Work Context, the procedures the host
    exports to composed modules (`ExchangeDelegatedOperationAudience`).

  ```go
  authority, err := moduleauthority.New(moduleauthority.Seams{
      Gateway:       gw,                                   // BaseURL() + HTTPClient()
      Authority:     moduleauthority.Authority{Address: authorityHostPort}, // TLS: nil = h2c
      InternalToken: internalToken,
      // Only from the consumer's own configured assertion that every
      // in-cluster hop is carried by a mutually authenticated mesh.
      AllowInsecureHTTP: meshProtected,
  }, moduleauthority.Credentials{Prefix: projectedPrefix, Secret: projectedSecret})
  if err != nil {
      return err // ErrInvalidSeams / ErrInvalidCredentials: fail closed at startup
  }
  defer authority.Close()
  child, err := authority.ExchangeOperation(ctx, moduleauthority.ExchangeRequest{
      BindingID: installedBindingID,
      Parent:    retainedParent,
      Lookup:    false,
  })
  ```

  `New` refuses a missing internal token, gateway base URL or authority
  address, and refuses to send the internal token or the module secret in
  plaintext to anything but `localhost` or a loopback IP unless
  `AllowInsecureHTTP` asserts the hop is mesh-protected. SaaS owns the installed
  binding's audience, scopes, lifetime, verification, and audit record.

  `ExchangeRequest` presents authority one of two ways, and exactly one — both,
  or neither, is `ErrInvalidExchange` before anything is sent. `Parent` is a live
  capability the caller holds; the child is attenuated against it and expires
  with it, so **work that outlives the parent cannot be authorized this way at
  all**. `DelegationID` is a reference to a host-owned, revocable source
  delegation, as `MintSourceOperationContext` reported it:

  ```go
  child, err := authority.ExchangeOperation(ctx, moduleauthority.ExchangeRequest{
      BindingID:    installedBindingID,
      DelegationID: delegationID, // no parent, and none needs to still be valid
  })
  ```

  No capability is presented on that arm and none is held. The host re-checks
  that the delegation is live, the source exists, the person still holds the
  role and the binding is unchanged, then mints that call's child. So it is not
  bounded by any token's remaining life and may be called again *after* the
  previous child has expired — which is the whole of the renewal path for work
  longer than a Work Context. A revocation takes effect on the next exchange.
  The reference is an identifier, never a credential: on its own it authorizes
  nothing, and the caller's own module Work Context still authenticates the call.

  Background work with no person present mints its own operation context
  instead. It carries exactly the binding's `headless_scopes`; a binding that
  declares none is refused with `ErrPermissionDenied`. It lives about a minute
  and is never cached, so mint one per call or short batch:

  ```go
  op, err := authority.MintModuleOperationContext(ctx, installedBindingID)
  // op.Token goes in x-codefly-work-context on the call to op.Audience.
  ```
- **`settings/`** — the schema-agnostic typed-settings library every module and
  product depends on instead of vendoring a copy. It has two parts:

  - **runtime** (`settings`) — presence-aware `Field[M, T]` access over a
    generated protobuf settings message, plus a `JSONCodec` that is the only
    boundary between typed settings and their sparse ProtoJSON storage. Product
    code works with typed fields and never traverses protobuf parents or JSON
    keys. `usersettings` below is the product's own generated field catalog (not
    shipped by this SDK); it is built once on top of these `settings.Field`
    helpers:

    ```go
    theme, err := usersettings.Fields.Appearance.Theme.Get(document)   // default when absent
    err = usersettings.Fields.Email.Product.Set(document, false)        // explicit false stays present
    ```

  - **renderers** (`settings/catalog`) — the reusable half of `module-compose`.
    A module declares its settings contributions and gets Go / TypeScript /
    proto catalogs from `catalog.RenderGo` / `RenderTypeScript` / `RenderProto`,
    instead of re-implementing the render functions in-repo.

  The runtime imports no generated schema, so it is byte-for-byte reusable
  across products; product-specific fields belong in the product proto and its
  typed field catalog, never here.

- **`workcontext/`** — the callee side of the Work Context contract. A service
  that receives a delegated `codefly.work-context/v1` capability (minted by
  accounts, forwarded by whoever acts on the user's behalf) verifies it here
  before trusting a claim: signature by `kid` against the accounts JWKS
  (`GET /v1/auth/.well-known/jwks.json`), `iss` = `saas-starter`, `aud` = the
  service's own name, the time window, and the type. sdk-go owns the wire
  format and the rotation-aware JWKS cache; this package binds them to the
  gateway seam, keeps the verified claims on the request context, and maps
  failures onto HTTP, Connect, and gRPC codes. Nothing fails open — no keys,
  an unreachable JWKS, or an unknown `kid` all reject the request.

  ```go
  verifier, err := workcontext.NewVerifier(workcontext.Config{
      Audience: "lastlogin",                     // this service's name; the token's aud must match
      Keys:     workcontext.JWKSFromGateway(gw), // the accounts JWKS behind the gateway
  })

  // net/http: 401 missing / invalid, 403 for another service's context
  mux.Handle("/api/", verifier.HTTPMiddleware(api))
  // Connect (unary and streaming handlers) and gRPC
  mux.Handle(lastloginv1connect.NewLoginsServiceHandler(svc,
      connect.WithInterceptors(verifier.ConnectInterceptor())))
  grpc.NewServer(grpc.ChainUnaryInterceptor(verifier.GRPCUnaryInterceptor()))

  // in a handler: the scope check reads the verified context off ctx — the
  // effective scopes are the last actor's attenuated grant; empty resource_ids
  // is the wildcard
  if err := workcontext.RequireScope(ctx, "lastlogin:logins", "read", loginID); err != nil {
      return nil, workcontext.ConnectError(err) // CodePermissionDenied
  }
  ```

  `RequireScopeHTTP("lastlogin:logins", "read")` guards a whole route, and
  `ScopesFromContext(ctx)` reads the effective scopes. Set `Config.Optional` to
  let a request without a context through unauthenticated (`FromContext` then
  reports false) — a present-but-invalid one is still rejected. `StaticKeys`
  pins a key set for tests or out-of-band distribution.

  Verification establishes authenticity and freshness but **does not enforce
  replay**: a captured token is accepted until it expires, whatever its
  `replay_policy`. Enforcing single-use needs a nonce store this stateless layer
  cannot own — a callee that requires it reads `FromContext(ctx).GetReplayPolicy()`
  and `GetNonce()` and rejects a nonce it has already seen against its own store.

## Versioning

This SDK's release version tracks the **saas-starter module version**
(`module/module.package.codefly.yaml`), recorded in `SOURCE.txt` alongside the
proto ref `gen/` was generated from.

## Regenerating `gen/`

The proto source of truth is `module-saas-starter/module/services/accounts/proto`.
To refresh (from a checkout of that repo):

```bash
cd module/services/accounts/proto
buf generate --template <this-repo>/buf.gen.yaml -o <this-repo>
```

`buf.gen.yaml` sets `managed.override.go_package_prefix =
github.com/codefly-dev/saas-sdk-go/gen`, so the descriptors are generated with
the correct module path. **Never hand-edit `gen/` or sed the module path** — the
protobuf file descriptors embed length-prefixed package strings and a text
rewrite corrupts them (panics at `init()`). Always regenerate.

> Wiring this regen into module-saas-starter's release (so a saas tag publishes a
> matching SDK tag) is tracked in the solutions EPIC (obin-ai/lodestar#53, item 4).

## Consuming

```go
import (
    "github.com/codefly-dev/saas-sdk-go/accounts"
    v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
)
```


### Exact executable artifact consent

`moduleauthority.Client` exposes `ApproveExecutableArtifact`,
`AuthorizeExecutableArtifact`, and `RevokeExecutableArtifact` on the resolved
accounts authority endpoint, using the ordinary cached module credential plus
an independently signed human parent. The first is an explicit administrator
activation action; the second only checks existing exact consent. Never approve
as a fallback on a refused run. Neither operation dispatches work or moves an
active pointer.

`ArtifactRequest` names the installation, installed policy ID and
`ArtifactIdentity`: its schema, source, complete canonical subject bytes,
ordered exact contract references and an int64 expected revision. The host
validates its installed `MODULE_PRINCIPALS[<module>].artifact_policies` ceiling.
The SDK preserves subject bytes and int64 values exactly, bounds the request,
and refuses mismatched or malformed authorization receipts. The module owns
canonical identity extraction, including every script/runtime and content pin;
the host does not interpret configuration or fabricate publisher attestation.

Consent belongs to the tenant and survives administrator turnover. The host
rechecks current caller authority, installation, source, policy and explicit
revocation on each run. Revocation addresses the approval UUID from the returned
`host/approved-artifacts/<uuid>` Ref, even if the policy was disabled. Revoked
exact identities are terminal; a newly selected content version or activation
revision needs explicit new consent. These Refs are not tool or artifact-access
credentials.

### Current installation identity

`moduleauthority.Client.GetCurrentInstallation(ctx, CurrentInstallationRequest)`
reads the host's current active, non-revoked installation identity. Supply the
original verified parent token and `InstallationID`. The client presents its own
module Work Context independently on the resolved authority endpoint. It never
forwards a viewer bearer, selects a tenant/source, or mints broader parent scopes.

The result is `CurrentInstallation{InstallationID, TenantID, SolutionIdentifier}`.
The SDK requires the exact requested ID and a nonempty, well-formed result;
the consumer must compare `TenantID` to its independently verified request tenant
and apply its own source naming rules. A successful observation is not executable
consent, a grant or durable liveness proof. Re-read for each new request.

The host preserves its organization-member metadata-read rule: no new permission
or binding is required. It checks current parent authority and delegation,
authenticated module/audience/tenant alignment, current membership, and exact
active record identity. A refusal never invokes a bearer-based fallback.

### Gateway authority transport and audit declarations

`moduleauthority.NewGatewayAuthority(seams, credentials)` explicitly selects
the host's binary-protobuf Connect authority bridge on the gateway base URL.
It refuses a simultaneous direct Authority address and does not fall back to
Accounts. The selected host must serve this bridge; a generated client alone
does not establish that a deployed host supports it. The existing `New`
constructor retains the direct authority transport.

`Client.DeclareAuditEventTypes` presents the module's event schemas through the
selected authority seam. Ownership and compatibility remain host decisions.
Public declaration types and enum constants are re-exported by moduleauthority.
