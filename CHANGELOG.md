# Changelog

## Unreleased

Core main and module-saas-starter v0.0.103. No Core tag carries the Core this
needs, so the floor is a pseudo-version; it moves to the first tag that does.

### Changed

- **Core floor: `v0.19.6-0.20261018051341-b81d92e6feda`** (Core main,
  2026-10-18), from v0.19.4 on this branch and v0.19.1 in v0.1.3. Core
  `22ddde98` removed the approved build from a Work Context: `SealSource` lost
  `ApprovedBuild` and `ErrNoApprovedBuild` is gone, so v0.1.3 does not compile
  for any consumer that requires that Core. It is also the Core the v0.0.103
  gateway requires. Core v0.19.5 still requires `SealSource.ApprovedBuild` and
  cannot build this release; MVS never selects it beside this floor.
- **`gen/` moves from `9ab36e7e` to released module-saas-starter v0.0.103**
  (`2cb00737`), with the existing plugin pins and buf v1.73.0. The tree
  reproduces byte-for-byte from that tag, and `SOURCE.txt` records it for both
  `gen-drift` and `conformance/hostgateway`. The module package version stays
  0.1.0; regeneration cuts no SDK release.
- **The module broker is the gateway's workload endpoint.** Host `af5cef9a`
  (module-saas-starter#1120) serves the three mints there alone — the public
  endpoint answers 404 — and `Seams.Gateway`'s documentation now says so. The
  same commit dropped the internal token from the work-context mint and relays
  accounts' `Unauthenticated` there as 401 (`ErrInvalidCredentials`) instead of
  502. The client is unchanged: it still sends the internal token on every
  mint, which the two operation mints still require. `conformance/hostgateway`
  serves the real gateway through the host's own `workloadListener`, and
  `moduleauthority`'s fake gateway follows the new perimeter.
- `datasource`'s response-encoding client moves to `internal/connectresponse`
  unchanged, so `accounts.Datasources()` reads responses through it too.

### Removed (breaking)

- **`(*moduleauthority.WorkContextSources).ApprovedBuild`**. Core's verifier
  no longer asks an issuer for an approved build — a change of approved builds
  revokes through the principal's epoch and the installation's revision — and
  its only correct "bears no execution" answer was the sentinel Core deleted.
  There is no replacement; a consumer calling it directly drops the call.
  `WorkContextSources` still implements Core's `SealSource`, `RevisionSource`
  and `GrantSource`.
- Generated, by the v0.0.103 regeneration, against v0.1.3:
  `ModuleMintWorkContextRequest.Upstream` and `.Manifest` (with their getters;
  the host stopped accepting a reported upstream and manifest), and
  `SolutionBackendBinding`, `SolutionRegistration.Backend`,
  `SolutionFrontendBinding.ContractVersion` and
  `SolutionRegistrationStatus_SOLUTION_REGISTRATION_STATUS_INCOMPATIBLE`, all
  in host `af5cef9a` (module-saas-starter#1120). No
  hand-written facade exposed any of them, and `moduleauthority` never sent
  `Upstream` or `Manifest`.
- Generated interfaces gain methods, which breaks a consumer that implements
  them: `DatasourceServiceClient`/`Handler.UpdateSource` and
  `SolutionRegistryServiceClient`/`Handler.GetRetainedSolutionFrontend`.
  Embedding the generated `Unimplemented…Handler` absorbs the handler half.

### Added

- **`accounts.Client.Datasources()`**, the `DatasourceService` facade beside
  `Audit()`: `ListSources`, `GetSource`, `AddSource`, `ListSourceOperations`,
  `DeclareSourceOperations`, `InvokeSourceOperation`,
  `LookupInvokeSourceOperation`, `BeginDatasourceAccountLink`,
  `CompleteDatasourceAccountLink` and `ListMyDatasourceAccountLinks`, bare
  messages in and out, the host's Connect error returned intact. Every message,
  enum, enum value and oneof wrapper they reach is re-exported from `accounts`
  under its generated name (`AddSourceRequest_Api`,
  `ApiOAuth2Config_GRANT_AUTHORIZATION_CODE`, …), so connecting and calling a
  host-owned API source no longer needs `gen/`. It is the raw contract;
  `datasource`'s `Invoke`/`Lookup` remain the effect-safe way to call an
  operation.
- `moduleauthority.Client.MintModuleWorkContext(ctx)` returns
  `ModuleContext{Token, ExpiresAt}` without caching, coalescing or retrying.
  `ExpiresAt` is the broker's reported expiry, not a decoded token claim.
  Existing `ModuleWorkContext(ctx) (string, error)` retains its cache and
  shared refresh through the same mint implementation; organization binding,
  bounded reads, Core-backed sealed-shape checking and safe refusals are shared.
- `BrokerTransportConnectionReset` classifies wrapped `syscall.ECONNRESET`
  through error identity with a fixed diagnostic, retaining no underlying
  cause. It is appended so existing transport category values remain stable;
  callers can choose to retry without accessing sensitive transport errors.
- Generated, by the v0.0.103 regeneration: `UpdateSource` (a connected source
  edited in place), `GetRetainedSolutionFrontend`, installation readiness
  (`InstallationReadiness`, `ReadinessCondition` and their enums), carried
  module access on team grants, `SolutionGroup`, `SourceFetchScope` and
  `CheckAuthorizationRevisionRequest.SealsNoBinding`. These add no
  hand-written facades.
- `internal/apiboundary` walks a oneof field's exported wrappers. The field's
  type is an unexported interface nothing can alias; the gate used to report
  that interface and stop, and now requires each wrapper a consumer writes to
  be re-exported (fixture `oneof`).

## v0.1.3 — served verifier sources

Tagged at `14fe024` (#44) without a heading here; these are the entries it
shipped.

### Added

- **`moduleauthority.WorkContextSources`**, the three live-authority sources a
  callee's verifier needs, over
  `ModuleCapabilitiesService/ResolveWorkContextAuthority`
  (module-saas-starter#1107). A composed module that receives a Work Context
  could previously only run core's forwarding-hop `Inspector` — no seal, no
  epoch, no revision, nothing consumed — so a capability whose installation was
  revoked, whose build was replaced, whose binding was withdrawn or whose
  principal's epoch had moved stayed accepted for its whole life. These
  implement `corework.SealSource`, `corework.RevisionSource` and
  `corework.GrantSource`, so a consumer hands one value to
  `sdk-go/workcontext`'s `NewVerifier` as all three and brings its own replay
  store (durable and shared when it runs more than one replica).

  This module still **verifies nothing**: it holds no key, reads no signature
  and decides nothing about a capability. A source is a client of the ISSUER's
  live state, which is this SDK's job; verification stays in
  `sdk-go/workcontext`.

  `WorkContextSources` is built per presented capability and takes exactly one
  required field, the capability itself — the host authenticates it, requires it
  to be addressed to the calling module and to its tenant, and answers only
  about the identifiers it names, so there is no tenant, audience or owner for a
  caller to supply or get wrong. Answers are translated and never interpreted: a
  state this client does not understand, an answer on another arm, and an answer
  about another principal, installation or binding are each
  `ErrUnreadableAuthorityAnswer`. Positive answers are cached for at most
  `AuthorityCacheBound` (2s, a ceiling rather than a default, because a cached
  answer delays a revocation by exactly that long); refusals are never cached.
  A host refusal arrives with its connect code, which is how a caller tells
  `CodeFailedPrecondition` ("the issuer decided; mint again") from
  `CodeUnavailable` ("the lookup failed; retry").

- **`moduleauthority.WorkloadTokenFile()`**, the one answer to "where is this
  module's projected token": the file `CODEFLY__WORKLOAD_TOKEN_FILE` names
  when it is set to a non-blank value, and the pod mount
  `/var/run/secrets/codefly/token` otherwise. Since codefly CLI v0.1.187
  (codefly-dev/cli#977) a native `codefly run` stands in for the cell and
  projects each module workload's token to a file it names in that variable,
  because a process cannot read the pod mount path; a module that hardcoded
  the mount minted nothing there. The variable and the path are exported as
  `WorkloadTokenFileVariable` and `PodWorkloadTokenFile`;
  codefly-dev/solution-runtime-go reads the same variable. It resolves the
  path only: the token is still read before every mint. Its scope is the
  services a native run names the variable for — `module-identity` without
  `workload-identity`; a `workload-identity` service receives its path as
  `TOKEN_FILE` in its `workload-identity` configuration and passes
  `ProjectedTokenFile(path)` itself.

### Changed

- **`gen/` moves from module-saas-starter v0.0.94 to `9ab36e7e`** (the merge
  commit of #1119 on `main`), which is where `ResolveWorkContextAuthority`
  first exists. It carries every accounts contract change made since v0.0.94 —
  `datasource`, `installations`, `api_keys`, `module_registration` and
  `work_contexts` all move — and those changes are the host's, not this SDK's.

- **`moduleauthority.ErrNoHostStateToMintFor`**, for HTTP **412** on the module
  Work Context and operation-context mints. The gateway at this ref relays
  accounts' `FailedPrecondition` as 412, where v0.0.94 relayed it as a 502 — so
  a workload the host admits that is installed in no organization it may mint
  for used to read as "the host is broken" and now reads as "the host works and
  there is nothing to mint for you yet". Nothing is wrong with the credential
  and nothing is denied; a mint succeeds once the module is installed. Found by
  `conformance/hostgateway` against the new ref, which is what that suite is
  for. The source mint's own 412 keeps its own meaning
  (`ErrDelegationMissing`).

### Removed (breaking)

- **`SessionId` is gone from three Work Context mint requests**, with their
  `GetSessionId()` accessors: `StartTaskWorkContextRequest`,
  `StartChildSessionWorkContextRequest` and `StartInstallationTaskRequest`. The
  host stopped accepting a caller-named session — every session a capability
  carries is now the one its caller authenticated with, or a fresh one the
  issuer assigns and returns — so there is no replacement field and nothing to
  set. A consumer that set one was naming a session it did not own; it drops the
  assignment. These are the only exported symbols the regeneration removed:
  nothing else in `gen/` lost a type, a field or an RPC.

## v0.1.2 — core v0.19.1; Work Contexts are core's, carried opaque

Closes #38. The SDK requires `github.com/codefly-dev/core v0.19.1` and no
longer depends on `github.com/codefly-dev/sdk-go/workcontext` at all: the
pre-cutover pseudo-version it pinned (`b3fb40d`) is retracted by
`workcontext/v0.1.0`, which also removed every symbol this SDK used, so any
build that also required v0.1.0 (solution-runtime-go, sdk-go v0.4.0) stopped
compiling. `gen/` and `SOURCE.txt` are unchanged (module-saas-starter v0.0.94).

### Removed (breaking)

- **`workcontext/` is deleted.** Callees verify with
  `github.com/codefly-dev/sdk-go/workcontext` directly. Removed with no
  replacement in this SDK: `NewVerifier`, `Config`, `Verifier` (`Verify`,
  `HTTPMiddleware`, `ConnectInterceptor`, `GRPCUnaryInterceptor`,
  `GRPCStreamInterceptor`), `KeySource`, `JWKS`, `JWKSFromGateway`,
  `JWKSURL`, `StaticKeys`, `Gateway`, `JWKSPath`, `DefaultIssuer`,
  `NewContext`, `FromContext`, `ScopesFromContext`, `HasScope`,
  `RequireScope`, `RequireScopeHTTP`, `ConnectError`, `GRPCError`,
  `HTTPStatus`, and `ErrMissing`, `ErrInvalid`, `ErrAudience`, `ErrDenied`,
  `ErrUnavailable`. Where sdk-go/workcontext v0.1.0 has a counterpart:
  `NewVerifier(VerifierSettings, KeySet)` → `*PinnedVerifier` (which needs the
  issuer's revision, seal, grant and replay sources), `HeaderName` /
  `FromHeaders`, `ResolveKeySetEndpoint` + `AcquireKeySet`, `ScopeContained`
  over `(*Verified).EffectiveScopes()`, and `ErrInvalid`, `ErrNotACoreToken`,
  `ErrRevoked`, `ErrReplayed`. It has no context helpers, no transport
  middleware, no error-to-status mapping, and no exported key-set path.
- **`moduleauthority` carries Work Contexts as `string`.** Every
  `codefly.WorkContextToken` in the public API is now the encoded string:
  `Client.ModuleWorkContext` and `Client.ExchangeOperation` return `string`;
  `ExchangeRequest.Parent`, `ArtifactRequest.Parent`,
  `ArtifactRevocation.Parent`, `CurrentInstallationRequest.Parent`,
  `OperationContext.Token` and `SourceOperationContext.Token` are `string`.
  Drop the `ParseWorkContextToken` before passing a parent and the
  `.Encoded()` after reading one.

### Behaviour change

- **Only core-format Work Contexts are carried.** Before caching or sending a
  token, `moduleauthority` asks `core/workcontext.Decode` whether it is
  structurally a sealed capability — it verifies nothing. A mint (broker or
  exchange) answering anything else is `ErrInvalidCapability`, and nothing is
  cached; a `Parent` core does not decode is `ErrInvalidExchange`,
  `ErrInvalidArtifact` or `ErrInvalidInstallation` before any request — not
  even the module's own mint — is sent. The old check accepted any
  `payload.signature` shape. **A host whose accounts still mints the
  pre-cutover JSON payload — every module-saas-starter release through
  v0.0.95, including the v0.0.94 `SOURCE.txt` records — has every mint
  refused**; this SDK needs a host carrying module-saas-starter#1093.
- The Work Context header is set from core's `wool.WorkContextHeader`
  (`X-Codefly-Work-Context`, the same name as before).

### CI

- `conformance/hostgateway` answers core-format tokens from its faked
  accounts and adds `TestSDKConformancePreCutoverTokenRefused`: the real
  v0.0.94 gateway relays a pre-cutover token on all three mints, and the SDK
  refuses each. The guarded `sdk-go/workcontext` now stays at the host's own
  pin in the conformance build, since this SDK no longer requires it.
## Unreleased — read the host's journal (waiting for the host release)

**`gen/` here is ahead of the ref `SOURCE.txt` records.** `ReadJournal` and its
three messages are not in `module-saas-starter` v0.0.94, and no release carries
them yet. The two generated files below were produced by this repo's own
`buf.gen.yaml` (buf 1.73.0, the pinned plugins) from the v0.0.94 proto with the
wire contract added to `module_capabilities.proto` in a scratch checkout, not
by hand. They are a stopgap: the `gen-drift` job fails until the contract is in
a published `module-saas-starter` release and `gen/` is regenerated from that
tag. Do not release this until it is.

### Added (moduleauthority, read the journal)

- `Client.ReadJournal(ctx, after, limit, types, deliveryBindings) (JournalPage, error)`
  calls `ModuleCapabilitiesService.ReadJournal` on the authority endpoint as the
  module, through the same credentials and refresh-once rule as every authority
  call. The host serves it only to a cross-tenant module principal; any other
  caller gets the host's `PermissionDenied`, preserved. Positions are opaque
  strings: `after` is exclusive, `""` reads from the oldest retained entry, and
  `JournalPage.NextAfter` is the resume point the host guarantees misses no
  committed entry and never passes an unfinished transaction, so a reader
  resumes from it rather than from the last `Position` it saw, and only stores
  and compares positions for equality.
- `JournalStart` (`"start"`) is the position that means "before the oldest
  retained entry": the host accepts it as `after`, and as the `from` of its
  delivery operation, and never returns it as an entry's position. A reader with
  no position of its own starts from it.
- `deliveryBindings` are the caller's binding ids whose mints and exchanges count
  as delivery machinery; the host sets `JournalEntry.DeliveryMachinery` on the
  entries that record one of them, or the caller's own module work-context mint,
  so a reader that delivers journal entries can tell a page of its own delivery
  from a page of real activity. A limit outside 0..500 (0 is the host's default of
  100), no type, or an empty type or binding id is `ErrInvalidJournalRead` before
  any call.
- Generated: `ReadJournal` on `ModuleCapabilitiesService`, with
  `ModuleReadJournalRequest`, `ModuleJournalEntry` and
  `ModuleReadJournalResponse`.

## v0.1.1 — the broker contract, held against the host's real gateway

- **Fix (moduleauthority).** A source mint's HTTP 412 returns
  `ErrDelegationMissing` again, wrapping the same `*BrokerError`: the source
  has no active delegation, and a person must reconnect it. It is decided by
  status alone, as 403 → `ErrDelegationInvalid` is — the v0.0.94 gateway
  answers 412 on that path only for `DELEGATION_MISSING` (any other
  FailedPrecondition is a 502) — so no response body is read.
  `ErrDelegationMissing` is no longer deprecated. `ErrDelegationRevoked`
  stays deprecated: telling it apart would need the body.
- **Behaviour change.** `moduleauthority.New` refuses an `OrganizationID` that
  is not a canonical (lowercase, hyphenated) UUID with `ErrInvalidCredentials`
  — accounts' own rule — whichever mints the client goes on to make. Who
  changes behaviour:
  - a client that mints module Work Contexts or operation contexts used to
    fail at its first such mint with an untyped HTTP 400; it now fails at
    construction;
  - a client that only mints source operation contexts, which never send the
    field, used to work with such a value; it now fails at construction.
    Leave `OrganizationID` empty there, or correct it.
  An uppercase, braced, `urn:uuid:`, unhyphenated or space-padded UUID is
  refused; the empty string still means "no organization".
- Documented: a missing or wrong organization (a shared module naming none, a
  tenant-pinned module naming another) is refused as `ErrInvalidCredentials`
  (HTTP 401) on the module Work Context mint and `ErrPermissionDenied`
  (HTTP 403) on the operation mint (`ErrInvalidCredentials` doc, README).
- **CI.** New `host-gateway-conformance` job: `conformance/hostgateway/run.sh`
  mints with the SDK's real client through module-saas-starter's real
  auth-gateway handlers at the `SOURCE.txt` ref, faking only accounts' gRPC
  mints. The host checkout comes from a `host-source` composite action now
  shared with `gen-drift`; `SOURCE.txt` records the gateway path beside the
  proto path. The runner fails unless all eight expected tests are listed,
  run and pass. It also fails if the build would give the gateway a grpc,
  protobuf or sdk-go/workcontext whose source differs from the host's pin,
  and it resolves nothing at `@latest`: `moduleauthority`'s imports must all
  be declared by the SDK's go.mod (checked `-mod=readonly` before `go get`
  adds it), and everything after that one `go get` runs `-mod=readonly`.
- `moduleauthority`'s fake gateway follows the real one's prefix rule, check
  order, and indifference to the legacy module-secret header; its remaining,
  deliberate divergences are listed in `host_fake_test.go`.

## v0.1.0 — callable sources

- Reject unsupported HTTP response encodings before reading the body or
  inferring an HTTP status: Declare/List return `DatasourceError` with no
  receipt; Invoke/Lookup keep `OutcomeUnknown` and the original effect ID.
  List returns `InputError` for an absent/unspecified host effect and
  `DatasourceError` for an unknown future effect enum, with no partial list.
- Fix cross-language parity at the error/receipt boundary: skip null and unknown
  details without losing effect IDs; scope `SOURCE_OPERATION_OUTCOME_UNKNOWN`
  to Invoke/Lookup; normalize UTF-8 charset, gzip case and BOM on Connect JSON
  errors while retaining the gateway client and Connect decoder.
- Add `DatasourceError{Receipt}` for invalid output on a valid COMMITTED receipt.
  Receipt output comes from `receipt.output_json`; result output comes from the
  top-level field. Both validate without discarding known commitment. Unknown
  receipts ignore output; invalid receipt identity/status/time stays unknown.
- Normalize absent listed schemas to `{}`; declaration still requires both
  schemas before dispatch. Use the protobuf Timestamp JSON profile for receipt
  times and rate-limit resets, rejecting year zero and excess fractional digits.
- Add `datasource.Client.Invoke`, `Lookup`, `DeclareOperations` and
  `ListOperations` on the existing gateway-bound Connect client, matching the
  Python facade. Add operations, JSON output, receipts, UUIDv7 effect IDs and
  per-call deadlines; the host alone owns replay and provider dispatch.
- Add `ErrNotPermitted`, `ErrUnknownOperation`, `InputError`, `RateLimited`,
  `ProviderRefused`, `OperationRefused`, `ErrEffectReused`, `ErrEffectNotFound`
  and `ErrOutcomeUnknown`. The host's `SOURCE_OPERATION_OUTCOME_UNKNOWN`
  reason retains the effect ID. `*OutcomeUnknown` wraps the original error for
  `errors.Is`/`errors.As` and Connect diagnostics; callers must check
  `ErrOutcomeUnknown` before applying transport retry rules. The SDK never retries.
  Only `SOURCE_PROVIDER_REFUSED` maps to a provider refusal; its status comes
  from the host's `provider_status` metadata (canonical HTTP 100–599, zero
  when absent or invalid). `InputError.Pointer` preserves the host's first
  `BadRequest.FieldViolation.field`. Host messages remain unchanged.
- Add `OperationDeclarationRemoved` / `ErrOperationDeclarationRemoved` for
  `SOURCE_OPERATION_DECLARATION_REMOVED`. It preserves the original effect ID
  and `receipt_status` as `ReceiptStatus` and `Receipt.Status` (COMMITTED or
  UNKNOWN; absent/unrecognized metadata becomes UNKNOWN). Recover with Lookup,
  never with a fresh ID; the SDK never retries or invents receipt output.
- Remove the old Lookup caveat: the host now recovers saved receipts after
  declaration removal/replacement, under current source read authority.
  Committed receipts survive; unresolved/expired stays unknown. Lookup NotFound
  means no attempt marker exists.
- Lookup misses return the original ID with `NOT_ATTEMPTED`; callers may
  explicitly reinvoke with the same ID. Committed receipts include `Output`,
  so recovery can retrieve output without another invoke. Receipt statuses are
  `COMMITTED`, `UNKNOWN` and `NOT_ATTEMPTED`.
- Every non-Connect HTTP failure after Invoke/Lookup dispatch, including 4xx,
  429 and 500, returns `ErrOutcomeUnknown` with the original effect ID and
  diagnostic cause. Decoded RPC errors retain their existing typed mappings.
- Validate effect IDs and reject an expired deadline before dispatch with
  `InputError`, no result and zero RPC calls. Explicit cancellation returns
  `context.Canceled` with no result. Add operation descriptions and host-computed
  digests to declaration/list round-tripping.
- Document the call and retry contracts in `README.md` and package examples.
- Invoke payloads use `input_json` / `output_json` strings containing exactly
  one JSON object, preserving numeric precision. Declaration schemas retain
  the host's `Struct` representation. Lookup sends only the effect ID; its
  existing org/source arguments cannot override the host's derived authority.
- Update `golang.org/x/tools` so the API boundary gate can read Go 1.27 export
  data; the gate and its fixtures are unchanged.
- Regenerate the full host API from released tag `v0.0.94`, recorded in
  `SOURCE.txt` with module version `0.1.0`. The datasource bindings are unchanged
  from the reviewed PR-head contract. This refresh creates no SDK release.
  CI and the regeneration procedure now accept release-tag provenance.
- Include the host's installation narrowing, organisation installation listing
  and team grant/revoke RPCs, plus platform-admin module envelope reads/writes,
  in the regenerated API. Module capability comments describe the host's
  structured tenant-membership and idempotency refusals.
- Include the runtime-facing `PruneSourceOperationReceipts` and
  `LookupPruneSourceOperationReceipts` RPCs in generated bindings only;
  no receipt retention methods are exposed by the caller facade.
- Re-export the OAuth2 grant type/constants reachable from source reads. The
  generation template emits all host-owned `saas/` schemas and imports Core's
  Runnable descriptors from Core v0.3.41, which supplies the required package
  while preserving consumer compatibility; plugin versions are unchanged.

### Removed / changed (breaking, inherited host contract)

- Host `v0.0.94` removes `MintModuleRegistration` and `MintSolutionRegistration`
  RPCs, their procedure constants, and `ModuleMintRegistrationRequest/Response`
  and `SolutionMintRegistrationRequest/Response`. Work Context mints now prove
  workload identity with projected ServiceAccount tokens; there is no replacement
  registration-credential mint RPC.
- Generated `ModuleMintWorkContextRequest`, `ModuleMintOperationContextRequest`
  and `ModuleMintSourceOperationContextRequest` lose `Secret`/`GetSecret` and
  gain `ServiceAccountToken`/`GetServiceAccountToken`. The first two requests
  also gain `OrganizationId` for shared-principal installation selection.
  The gateway route-rejection fixture uses the current request field. The
  hand-written broker sends the projected token as its sole Bearer credential,
  and includes the organization selector on the first two REST mint bodies.
- `moduleauthority.CurrentInstallation.SolutionIdentifier` is replaced by
  `TargetID` and `BindingID`, reflecting the host's immutable authority
  identities. The SDK validates the target UUID and nonempty binding.
- Generated `Installation.SolutionIdentifier`,
  `InstallSolutionRequest.SolutionIdentifier` and
  `ModuleCurrentInstallationResponse.SolutionIdentifier` are removed in favor
  of target/binding identity fields. Their `GetSolutionIdentifier` methods
  disappear too.
- Generated `PutSolutionRegistration` / `DeleteSolutionRegistration` RPCs,
  procedure constants and request types (including the Put request's oneof
  wrappers), `SolutionFrontendRegistration` and `SolutionBackendRegistration`
  are removed. The host reconciles delivered presence; there is no writer RPC
  replacement. Read contracts and new entitlement/target contracts are retained.
- Generated `SolutionFrontendBinding.LeaseExpiresAt`,
  `SolutionBackendBinding.LeaseExpiresAt`, `SolutionRegistration.RuntimeBoundary`
  and their getters, and the
  `SolutionRegistrationStatus_SOLUTION_REGISTRATION_STATUS_EXPIRED` constant
  are removed by the same delivered-presence contract. No generated output was
  edited or restored to retain the older API.

## Unreleased — exclude broker diagnostics

- **Fix at the SDK error boundary; breaking.** Broker errors exclude all
  response diagnostics instead of attempting credential redaction.
  `BrokerError.Path` and `Reason` are removed; only the HTTP status and the
  refused/not-established/other classification remain, with fixed messages.
- Transport and response read failures return `BrokerTransportError` with only
  a timeout/connection-refused/TLS/other category. No underlying cause is
  retained or unwrapped; retry policies use `errors.As` and the category.
- Non-200 status classification does not read the response body. Source 403s
  uniformly return `ErrDelegationInvalid`; 412s retain only `BrokerError`.
  Body-derived `ErrDelegationMissing` and `ErrDelegationRevoked` are deprecated
  and no longer returned by source mints.

## Unreleased — mint once: the projected ServiceAccount token replaces the module secret

- **Breaking.** `moduleauthority.Credentials` is `{Prefix, Token, OrganizationID}`.
  `Token` is a `ProjectedTokenSource` that reads the module pod's projected ServiceAccount
  token (audience `accounts`). `Secret` is deleted with the host's
  `MODULE_IDENTITY_SECRETS`. Every broker mint sends the token as
  `Authorization: Bearer`, read from its source before that mint (the kubelet
  rotates it); `X-Codefly-Module-Secret` is no longer sent.
- `OrganizationID` selects the installation for module Work Context and
  operation-context mints. Shared modules must supply it; tenant-pinned modules
  may omit it or name their own tenant. Use a separate client per organization
  so its cached module Work Context stays scoped. Source-context mints send only
  `prefix` and `source_id`; their delegation determines the organization.
- Every token source is trimmed and checked before dispatch: empty or oversized
  (more than 64 KiB) tokens return `ErrInvalidCredentials` with zero broker calls.
  The host requires audience `accounts` and lifetime at most 600 seconds. No
  token path is defaulted. Tests cover file rotation and exact REST bodies on
  all three mints; the host fake rejects missing, duplicate and non-Bearer
  Authorization, and any legacy secret header as a regression guard.
- Added `ProjectedTokenFile`, the file-backed source. Its method set is
  `sdk-go/workcontext`'s `ProjectedTokenSource`, so that implementation is
  accepted once this module moves off the pre-cutover `sdk-go/workcontext` pin.
- Added `ErrExecutionUnestablished` for the broker's `503`: the host could not
  establish what the pod is running, nothing was decided, and a retry may
  succeed. `401` still maps to `ErrInvalidCredentials`.
- The broker client speaks the REST contract verified against
  `gateway_modules.go` at released host tag `v0.0.94`; the generated request
  types are consumed through the tagged API refresh above.

## Unreleased — module audit declarations and gateway authority

- Added `Client.DeclareAuditEventTypes` over the existing module-authority
  credential cache and refresh path, with declaration types and enum constants
  re-exported from `moduleauthority`. Host admission rules remain authoritative.
- Added explicit `NewGatewayAuthority` for hosts serving the binary-protobuf
  Connect bridge. Existing direct and mint-only clients retain their behavior.

## Unreleased — module audit emission

- Added `moduleauthority.Client.EmitAuditEvent`, preserving the complete event,
  caller-selected idempotency key and host error codes through the existing
  authenticated authority transport. Uncertain effects are not retried.
- Kept main's complete generated contract and existing artifact, installation
  and delegation APIs. Receipt lookup and runtime-boundary attestation remain
  deferred until their host contract is integrated. Module version stays 0.1.0.
- Updated the API boundary importer for Go 1.27.2 export data.

## Unreleased — current installation identity

- Added `moduleauthority.Client.GetCurrentInstallation` with independent module
  credentials and parent proof, exact installation selector and an identity-only
  active/non-revoked response. Denials do not fall back to viewer credentials.
- Regenerated the complete API from host `21f139ee4`, retaining delegation-reference
  exchange and executable-artifact approval. The source also adds the current
  platform catalogue read; no generated files were selected or discarded.


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

### Added (moduleauthority, delegation-reference exchange)

- `ExchangeRequest.DelegationID` presents a host-owned, revocable source
  delegation instead of a parent capability, on the arm
  `ExchangeDelegatedOperationAudience` already serves. Nothing long-lived is
  minted: the reference is an identifier, the host re-checks the delegation on
  every call and mints a fresh short child, and the caller's own module Work
  Context still authenticates the request. Because no capability is presented,
  the arm is not bounded by any token's remaining life and may be called again
  after the previous child has expired — which is how work longer than a Work
  Context renews. `ExchangeRequest` now requires exactly one of `Parent` and
  `DelegationID`; both, or neither, is `ErrInvalidExchange` before anything is
  sent, matching the host's message-level rule.

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
    `Authorization: Bearer <projected ServiceAccount token>`;
  - `ExchangeOperation` calls accounts' `authority` gRPC endpoint with the
    module Work Context in `x-codefly-work-context` and the internal token in
    `x-codefly-internal-token`, and still refreshes a rejected module Work
    Context once.

  `New` fails closed with the new `ErrInvalidSeams` on a missing internal
  token, gateway, gateway base URL or authority address, and on a plaintext
  seam to anything but `localhost` or a loopback IP unless the consumer sets
  `AllowInsecureHTTP` from its own mesh-protection assertion; and with
  `ErrInvalidCredentials` on an empty prefix or missing token source. The broker
  client never follows a redirect. The `connect.ClientOption` variadic is gone.

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
