---
name: api-boundary-triage
description: Diagnose and fix a failing internal/apiboundary gate in codefly-dev/saas-sdk-go — the test that keeps the generated gen/ tree out of this module's public API. Use when go test ./... reports "is reachable from the public API via ... but is not re-exported", "is re-exported as ... but its constant ... is not", "no public packages discovered", or when TestGateCatchesKnownBypasses fails after a change to the gate or its fixtures. Also use before choosing how to expose a newly generated type from accounts/ or datasource/, since the same rule decides whether an alias, an unexported field, or nothing at all is correct. Explains what each finding means, the one real fix, and the five changes that turn the gate green without making it true.
---

# Triaging the public-API boundary gate

`internal/apiboundary` asks a type question: walk every exported symbol of
every public package, and report any type from `saas-sdk-go/gen/` a consumer
can reach. Reaching one means the consumer must import the stub tree to name
it, which pins `gen/` as public API — and `gen/` is regenerated whenever the
contract moves, so it cannot be.

Run it alone while working:

```bash
go test ./internal/apiboundary/ -v
```

Two tests. `TestPublicAPINamesEveryTypeItExposes` runs the rule over this
module. `TestGateCatchesKnownBypasses` runs it over `testdata/fixture`, a
module built to break it in every way an earlier syntactic version could be
broken — it is what distinguishes a gate that found nothing from a gate that
has stopped looking.

## Reading a finding

Each finding names the package, the generated type, and the path that reached
it (`accounts.AuditClient.QueryAuditLog() result.Events[]`). Follow the path:
the last hop is the exported func, var, const, struct field, or method that
puts the type in front of a consumer.

- **`not-re-exported`** — the type is reachable and no public package aliases
  it. Fix: add it to that package's `types.go`.
- **`missing-constant`** — the enum type is aliased but a constant is not. A Go
  type alias carries the type and *not* the package-level constants declared
  with it, so a consumer can hold the value and cannot write the comparison.
  Fix: re-export the constants too.
- **`no public packages discovered`** — not a boundary violation. The loader
  found nothing, which means the module failed to load. Fix the build first.
- **a `load <pkg>` error** — same: the gate refuses to report a vacuous pass.

Re-exports are pooled across public packages: if `datasource` aliases
`Datasource`, `accounts` may expose it too. Alias it wherever it reads best.

## The fix

In the facade package's `types.go`:

```go
type (
    // Datasource is one entry of a ListSourcesResponse.
    Datasource = v1.Datasource
)

const (
    DatasourceStatusActive = v1.DatasourceStatus_DATASOURCE_STATUS_ACTIVE
)
```

The alias is the same type, so it costs nothing at runtime and breaks no caller
that already names the generated package. Then re-run the gate: a fix for one
finding often uncovers the types reachable *through* the newly named one.

## What is not a fix

Each of these makes the gate green. None makes the statement true.

1. **Moving the facade under `internal/`.** `loadPublicPackages` skips
   `internal`, so the package stops being examined. The types are still exposed
   wherever the facade re-emerges.
2. **Widening the skip rules** in `loadPublicPackages`, or adding the offending
   package to them.
3. **Weakening the walker** — dropping the recursion into struct fields,
   methods, or type parameters. The fixtures exist because a narrower walk was
   already shipped once and missed real violations.
4. **Editing the `want` map in `gate_test.go`,** or deleting a fixture, to make
   `TestGateCatchesKnownBypasses` agree with a gate that now finds less.
5. **Telling consumers to import `gen/`.** That is conceding the invariant, not
   satisfying it.

**Unexporting is the ambiguous one.** An unexported field holding a generated
client — `inner accountsv1connect.AuditServiceClient` — is how a facade is
*meant* to be built: a consumer can neither read nor set it, so the path never
reaches their source, and the gate is right to stop there. Unexporting
something a consumer actually needs is the hack wearing that pattern's clothes.
Ask which one you are doing, and say so in the PR.

If the honest answer is that the type cannot be aliased — it is generated into
another module, say — then the gate has found a real design problem, and the
fix is upstream in whoever owns that type. Say that in the PR rather than
quietly making the surface smaller.
