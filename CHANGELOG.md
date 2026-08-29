# Changelog

## Unreleased (targets 0.1.0)

Regenerated `gen/` from `module-saas-starter` main (see `SOURCE.txt` for the
exact ref). The version is bumped to `0.1.0` to match the saas-starter module
version and to signal the breaking removal below.

### Added
- `saas.accounts.v1.DatasourceService` bindings and a `datasource` facade
  (`datasource.New(gw).AddGitHubSource / .ListSources / .Sync`).
- `settings` — the schema-agnostic typed-settings runtime promoted out of
  `module-saas-starter` (`pkg/settings`): presence-aware `Field[M, T]` access
  and a `JSONCodec` for sparse ProtoJSON storage. Modules depend on this
  package instead of vendoring a copy.
- `settings/catalog` — the reusable catalog renderers (`RenderGo`,
  `RenderTypeScript`, `RenderProto`) that `module-compose` calls to emit
  Go / TypeScript / proto settings catalogs from declared contributions.

### Removed (breaking)
- `AuditExportService` client (`accountsv1connect.AuditExportServiceClient`,
  `NewAuditExportServiceClient`) and the `AuditExportJob` type
  (`saas/exports/v1`). The audit-export proto was deleted upstream — its server
  surface no longer exists — so the generated client is gone too. Callers that
  imported these will not compile against `0.1.0`; there is no drop-in
  replacement because the feature was removed, not renamed. `AuditService`
  (`QueryAuditLog`, `AggregateAuditLog`) is unaffected.
