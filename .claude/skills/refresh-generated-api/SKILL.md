---
name: refresh-generated-api
description: Move the generated gen/ tree in codefly-dev/saas-sdk-go to a newer codefly-dev/module-saas-starter proto ref, and update the four things that move with it — facade aliases, SOURCE.txt, CHANGELOG.md, and the version. Use when a proto change has landed upstream and this SDK has to carry it, when a consumer needs a service or field that gen/ does not yet have, when SOURCE.txt is behind the contract you are coding against, or when you need to prove the committed gen/ tree really reproduces from its recorded ref. Covers getting the proto at an exact ref, the buf invocation and why its plugin pins are load-bearing, the clean flag that makes upstream deletions land as breaking removals, and the byte-for-byte reproduction check.
---

# Refreshing `gen/` from the accounts proto

`gen/` is output. The source of truth is
`codefly-dev/module-saas-starter` at `module/services/accounts/proto`, and
`SOURCE.txt` records the exact ref this tree was generated from. Never
hand-edit `gen/` and never rewrite the module path with `sed`: the descriptors
embed length-prefixed package strings, so a text rewrite corrupts them and
panics at `init()`.

## 1. Get the proto at the ref you want

A sparse checkout is enough — the proto directory is self-contained:

```bash
git clone --filter=blob:none --no-checkout \
    https://github.com/codefly-dev/module-saas-starter.git starter
cd starter
git sparse-checkout set --no-cone 'module/services/accounts/proto'
git checkout <ref>          # a commit SHA, so the result is reproducible
```

Put it outside this repo's tree — a scratch directory, not a sibling path a
`./...` pattern can pick up.

## 2. Generate

```bash
repo="$(git -C <path-to-saas-sdk-go> rev-parse --show-toplevel)"
cd starter/module/services/accounts/proto
buf generate --template "$repo/buf.gen.yaml" -o "$repo"
```

Use **this** repo's `buf.gen.yaml`, not the one beside the proto. It sets
`managed.override.go_package_prefix = github.com/codefly-dev/saas-sdk-go/gen`,
which is what makes the descriptors carry this module's path. `out: gen` in the
template is relative to `-o`, so `-o "$repo"` writes `$repo/gen`. Takes about a
second.

Two properties of that template matter:

- **`clean: true` wipes `gen/` first.** So a proto deleted upstream disappears
  here rather than lingering — which is how `AuditExportService` left in 0.1.0.
  Read `git status` after generating as a diff of the *contract*, and treat
  every deletion as a breaking removal for consumers.
- **The plugin versions are pinned** (`protoc-gen-go@v1.36.11`,
  `protoc-gen-connect-go@v1.20.0`). That pin is what makes the output
  byte-reproducible from a ref. Bumping it rewrites the whole tree, so do it as
  its own change with its own reasoning — never incidentally inside a contract
  refresh.

## 3. Re-export what the facades now expose

New or changed messages reachable from `accounts/` or `datasource/` need alias
re-exports in that package's `types.go`, and generated enums need their
constants too. `internal/apiboundary` decides this, not judgement:

```bash
go test ./internal/apiboundary/ -v
```

Use the `api-boundary-triage` skill for a failing run — in particular for which
of the several green-making changes is the real fix.

## 4. Record the ref, the removals, and the version

Three files move with the tree, and a tag that skips one claims a contract it
does not carry:

- **`SOURCE.txt`** — the ref you checked out, and the module version from
  upstream's `module/module.package.codefly.yaml`.
- **`CHANGELOG.md`** — under the unreleased heading. `### Added` for new
  services, facades, and fields; `### Removed (breaking)` for anything the
  regeneration deleted, naming the symbols a consumer will fail to compile
  against and whether a replacement exists.
- **the release version** — this SDK's version tracks the saas-starter *module*
  version, not its own cadence.

## 5. Verify

```bash
go build ./... && go vet ./... && go test ./... && go test -race ./...
gofmt -l . | grep -v '^gen/'     # expect no output
```

To prove the tree in a PR is exactly what the recorded ref generates — rather
than something with a hand-edit in it — generate the recorded ref into a scratch
directory and diff:

```bash
buf generate --template "$repo/buf.gen.yaml" -o /tmp/regen-check
diff -r /tmp/regen-check/gen "$repo/gen"
```

Silence is the proof. This also catches a stale `SOURCE.txt`: if the diff is
non-empty for a ref you did not change, the recorded ref is not the one the
committed tree came from, and that is worth its own issue rather than a quiet
overwrite.

## Scope

Wiring this into module-saas-starter's release, so a saas tag publishes a
matching SDK tag, is tracked in the solutions EPIC (obin-ai/lodestar#53, item
4). Until it lands, this walk is manual — which is a gap in the tooling, not a
step to get good at performing.
