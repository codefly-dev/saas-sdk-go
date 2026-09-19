# Changelog

## Unreleased (targets 0.1.0)

Regenerated `gen/` from `module-saas-starter` main (see `SOURCE.txt` for the
exact ref). The version is bumped to `0.1.0` to match the saas-starter module
version and to signal the breaking removal below.

### Added
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
