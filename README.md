# saas-sdk-go

The **Go SDK for the saas accounts API** — a versioned, published client that
solutions depend on instead of regenerating and vendoring their own `gen/` tree.

Two layers:

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
      AccessToken: token,
  })
  _, err = ds.Sync(ctx, org, src.GetId())
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
