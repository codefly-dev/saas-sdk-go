# Changelog

## Unreleased — exact executable artifact consent

### Added
- Module authority methods for explicit approval, live exact authorization and
  terminal revocation, with canonical subject bytes and exact int64 revisions.
- Regenerated the complete API from host da6d1b2, including current installation,
  organization, invitation and datasource contracts. No generated file was curated.


## Unreleased (targets 0.1.0)

Regenerated `gen/` from `module-saas-starter` main (see `SOURCE.txt` for the
exact ref). The version is bumped to `0.1.0` to match the saas-starter module
version and to signal the breaking removal below.

### Added (moduleauthority, notify one person)

- `Client.NotifyUser(ctx, UserNotice) (bool, error)` calls
  `ModuleCapabilitiesService.NotifyUser` on the authority endpoint as the
  module, through the same credentials and refresh-once rule as every authority
  call. The host checks the recipient is a member of the tenant and routes the
  notice through their category policy; the result is whether it was delivered
  (false when an optional category was switched off). A notice without a
  tenant, recipient, title or category is `ErrInvalidNotice` before any call.

### Added (moduleauthority, mint-only clients)

- `Seams.Authority` may be left zero: `New` then builds a mint-only client. The
  broker's mints (`ModuleWorkContext`, `MintModuleOperationContext`,
  `MintSourceOperationContext`) work, and every call that needs accounts'
  authority endpoint fails with the new `ErrNoAuthority` before any request. A
  module that only mints no longer has to hand the SDK an address it never
  dials. An authority seam that is set but incomplete is still
  `ErrInvalidSeams`.

### Changed (breaking)

- `moduleauthority` now reaches the host through the two seams it actually
  serves a composed module, and its constructor takes them explicitly:
  `New(Seams{Gateway, Authority, InternalToken, AllowInsecureHTTP},
  Credentials) (*Client, error)`, plus `Client.Close()`. Before this, every
  call was a Connect RPC to `<gateway>/saas.accounts.v1.ModuleCapabilitiesService/<Method>`,
  which the host's gateway never serves (its metadata refuses to expose any
  `EXPOSURE_INTERNAL` procedure at the edge, and a live gateway answers 404
  "endpoint not exposed"), so every consumer failed against a real host; the
  tests passed only against a fake ModuleCapabilitiesService. Now:
  - the three mints go to the gateway's module broker as JSON
    (`POST /modules/_work-context`, `/modules/_operation-context`,
    `/modules/_source-operation-context`) with `X-Codefly-Internal-Token` and
    `X-Codefly-Module-Secret`;
  - `ExchangeOperation` calls accounts' `authority` gRPC endpoint with the
    module Work Context in `x-codefly-work-context` and the internal token in
    `x-codefly-internal-token`, and still refreshes a rejected module Work
    Context once.

  `New` fails closed with the new `ErrInvalidSeams` on a missing internal
  token, gateway, gateway base URL or authority address, and on a plaintext
  seam to anything but `localhost` or a loopback IP unless the consumer sets
  `AllowInsecureHTTP` from its own mesh-protection assertion; and with
  `ErrInvalidCredentials` on an empty prefix or secret. The broker client never
  follows a redirect. The `connect.ClientOption` variadic is gone.

  Refusals change shape with the transport: a broker answer other than 200 is
  a `*BrokerError` (path, status, reason), wrapped with `ErrInvalidCredentials`
  on 401 and the new `ErrPermissionDenied` on an operation mint's 403 (which was
  `connect.CodePermissionDenied`). The source mint maps 412
  `DELEGATION_MISSING` to `ErrDelegationMissing`, 403 `DELEGATION_REVOKED` to
  `ErrDelegationRevoked`, and every other 403 — `DELEGATION_INVALID`, or a body
  naming no reason — to `ErrDelegationInvalid`; it no longer reads a
  `google.rpc.ErrorInfo`. Consumers (one runtime module and one document-store
  module) move with this release.
- The SDK now builds on `github.com/codefly-dev/sdk-go/workcontext` instead of
  the root `github.com/codefly-dev/sdk-go` package, whose Work Context API
  moved to that module in sdk-go v0.2. `moduleauthority` and `workcontext`
  return and accept `workcontext.WorkContextToken`; a consumer on sdk-go v0.2
  could not compile `moduleauthority` before this. A consumer still on sdk-go
  v0.1 root types must move with it.
- `workcontext` reports a key set it could not fetch as the new
  `ErrUnavailable` (HTTP 503, gRPC/Connect `Unavailable`), not `ErrInvalid`
  (401): the token was never judged, so a caller retries rather than being told
  its credential is bad. No claims are ever returned either way; a key set
  that is served but unusable is still `ErrInvalid`.

### Added

- `moduleauthority.Client.MintSourceOperationContext(ctx, sourceID)`: a
  short-lived Work Context minted through the active delegation a person
  recorded when they connected a datasource source. It acts in the source's
  organization, is owned by that person, and names the module as the sole
  actor; SaaS derives the organization, audience and scopes from the
  delegation, so the caller names only the source. A refusal is typed as
  `ErrDelegationMissing` (reconnect the source), `ErrDelegationRevoked` or
  `ErrDelegationInvalid`; nothing else is claimed to be one. Returns
  `SourceOperationContext`; `ModuleMintSourceOperationContextResponse` is
  re-exported.
- `gen/` regenerated from module-saas-starter `2c35828` (source delegations,
  #940): `ModuleCapabilitiesService/MintSourceOperationContext`,
  `DatasourceService/ListSourceDelegations` and `RevokeSourceDelegation`, and
  the directory service that landed upstream since `2637cbb`.
- `moduleauthority.Client.MintModuleOperationContext` — mints, with no person
  present, a Work Context for one installed operation binding's
  `headless_scopes` (`ModuleCapabilitiesService/MintModuleOperationContext`).
  Never cached; rejects a response that is expired or names another binding.
- `gen/` regenerated from `module-saas-starter` commit
  `5ec93916b8576fbd97ebf842176fa2a437670114` (the merge of #905, the PR adding that RPC), which also brings in
  the accounts contract changes landed since `b741ab52`: registered clients
  (`ClientRegistryService`), and the authentication, authorization, audit, teams
  and common message changes.
- `moduleauthority` — a gateway-bound facade for module Work Context minting
  and installed operation-authority exchange. It refreshes the short-lived
  module capability before expiry, retries one rejected module capability,
  returns opaque `sdk-go` Work Context tokens, and never retains parent or
  exchanged operation capabilities.
- Current accounts contracts through `module-saas-starter` commit
  `b741ab52386a35f4e847a4e75952bcb3089a80cf`, including module capabilities,
  installations, accessible scopes, dashboards, resource follows, solution
  registration, composed organization settings, and SaaS events.
- `saas.accounts.v1.DatasourceService` bindings and a `datasource` facade
  (`datasource.New(gw).AddGitHubSource / .ListSources / .Sync`).
- Datasource boundary selection and a `FileExtensions` suffix allowlist on
  `GitHubSource`, so callers can restrict ingestion (for example to `.md`) via
  the owned host contract rather than filtering in a solution.
- `settings` — the schema-agnostic typed-settings runtime promoted out of
  `module-saas-starter` (`pkg/settings`): presence-aware `Field[M, T]` access
  and a `JSONCodec` for sparse ProtoJSON storage. Modules depend on this
  package instead of vendoring a copy.
- `settings/catalog` — the reusable catalog renderers (`RenderGo`,
  `RenderTypeScript`, `RenderProto`) that `module-compose` calls to emit
  Go / TypeScript / proto settings catalogs from declared contributions.
- `workcontext` — callee-side Work Context verification. A `Verifier` over
  sdk-go's rotation-aware JWKS verifier, bound to the accounts JWKS through the
  gateway seam (`JWKSFromGateway`) with the callee's service name pinned as
  `aud`; `HTTPMiddleware`, `ConnectInterceptor` (unary + streaming handlers),
  and `GRPCUnaryInterceptor` / `GRPCStreamInterceptor` that store the verified
  claims on the request context (`FromContext`); `ScopesFromContext`,
  `HasScope`, `RequireScope`, and `RequireScopeHTTP` for handler-level scope
  checks. Fails closed on no keys, an unreachable JWKS, or an unknown `kid`.
  Adds `github.com/codefly-dev/sdk-go` (with `core` and `grpc`) to the module
  graph. Boot-time warm-up via the verifier's `Refresh` and a distinct outage
  (503) status are deferred until codefly-dev/sdk-go#5 and #6 land.

### Removed (breaking)
- The obsolete datasource `target_collection` field. `GitHubSource.Collection`
  remains source-compatible and now requests a host-owned collection boundary;
  callers may instead select an existing boundary with `BoundaryNodeID`.
- `AuditExportService` client (`accountsv1connect.AuditExportServiceClient`,
  `NewAuditExportServiceClient`) and the `AuditExportJob` type
  (`saas/exports/v1`). The audit-export proto was deleted upstream — its server
  surface no longer exists — so the generated client is gone too. Callers that
  imported these will not compile against `0.1.0`; there is no drop-in
  replacement because the feature was removed, not renamed. `AuditService`
  (`QueryAuditLog`, `AggregateAuditLog`) is unaffected.
