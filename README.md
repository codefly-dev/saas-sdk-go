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
