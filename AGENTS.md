# Working in codefly-dev/saas-sdk-go

`github.com/codefly-dev/saas-sdk-go` (Go 1.27) is the **published SDK** that
solutions and modules depend on instead of regenerating and vendoring their own
copies. Four surfaces: the generated accounts bindings behind hand-written
facades (`accounts/`, `datasource/`), the schema-agnostic typed-settings runtime
and its catalog renderers (`settings/`), and callee-side Work Context
verification (`workcontext/`).

It does **not** own:

- the accounts **proto**. That is `codefly-dev/module-saas-starter`
  (`module/services/accounts/proto`); `gen/` here is output, never source.
- the Work Context **wire format** or the rotation-aware JWKS verifier. Those
  are `codefly-dev/sdk-go`; `workcontext/` only binds them to the gateway seam.
- the resource and configuration model (`codefly-dev/core`), the gateway
  (`codefly-dev/solution-runtime-go`), or any product's settings schema.

A change that belongs to one of those gets made there and consumed here. This
is a library: a defect is always observed somewhere downstream, so the pull
toward absorbing it locally is constant and the rules below exist against it.

## How to behave

Fleet standard — [handbook#68](https://github.com/obin-ai/handbook/issues/68).

- **A gap in the tooling is a bug in the tooling — never a reason to reach
  around it.** When something needs a step `buf`, `codefly`, or the upstream
  module does not perform, the answer is a capability fixed in whichever tool
  owns it, named in the PR. Never a hand-assembled substitute — not as a
  "workaround", not "just this once", not "until the capability lands".
- **Never hack. Provide the best fix, even when it spans repos.** The fix
  living in `module-saas-starter` or `sdk-go` is not a reason to work around it
  here. Open the PR there and consume the reviewed result. When it genuinely
  cannot be fixed now, the deliverable is a precise issue against that owner
  plus an explicitly labelled stopgap — never an unlabelled one.
- **Classify every change that makes something work**, in the PR body: a *fix*
  at the place that owns the behaviour, or a *hack*. A hack does not become a
  fix by working, by being small, by being local, or by the real fix belonging
  elsewhere. Hand-editing `gen/` is the canonical hack here, and it is the one
  that looks most like a fix.
- **Never hardcode what the system resolves** — injected environment, derived
  ports, service addresses, credentials copied out of another component. This
  SDK is on the resolving side of that line: a facade takes a gateway and reads
  `BaseURL()` off it rather than holding a URL, and `JWKSFromGateway` derives
  the JWKS address the same way. Typing one of those in encodes something true
  on one machine for ten minutes, and it fails quietly — a runtime missing a
  credential can skip registration *silently*, so the service boots, serves,
  and is simply absent.
- **Diagnose, do not pattern-match.** "It started working when I set X" is not
  a diagnosis — set X back and confirm it breaks. Do not trust an error message
  before checking its claim: a `gen/` panic at `init()` reporting a malformed
  descriptor has meant a text-rewritten module path, not a bad protobuf
  release.
- **Say what you did not verify.** Unverified is not the same as working. This
  repo has no CI (below), so nothing catches the difference for you — if you
  could not exercise something, the PR says so.

## Build and test

**This repo has no CI.** There are no workflows and no PR has ever reported a
check, so this walk is the entire gate and it runs only if you run it. Every
command here was run against this tree, from a clean checkout:

```bash
go build ./...
go vet ./...
go test ./...                    # ~0.4s; the real gate lives here
go test -race ./...
gofmt -l . | grep -v '^gen/'     # expect no output
```

`go test ./...` is load-bearing beyond the unit tests: `internal/apiboundary`
type-checks the entire public surface against the boundary rule below, and
`internal/agentcontext` checks these context files. There is no lint config —
`golangci-lint` being installed on your machine does not make it a gate here.

Nothing runs this walk on a PR. A workflow that does is worth filing; until one
exists, a reviewer cannot distinguish a run walk from a skipped one, which is
why the PR template asks what you did not verify.

## Where things live

| Path | Owns |
| --- | --- |
| `accounts/` | gateway-bound facade over the accounts services; `types.go` holds its alias re-exports |
| `datasource/` | the same facade over `DatasourceService` |
| `settings/` | presence-aware `Field[M, T]` runtime; `JSONCodec` is the only typed↔ProtoJSON boundary |
| `settings/catalog/` | Go / TypeScript / proto catalog renderers that `module-compose` calls |
| `workcontext/` | callee-side verification: HTTP, Connect, gRPC middleware plus scope checks |
| `gen/` | generated output. Regenerated, never edited |
| `internal/apiboundary` | the public-API boundary gate, with fixtures that prove it still looks |
| `internal/agentcontext` | the budget and frontmatter contract for this file and the skills |

`README.md` covers what each surface does for a *consumer*, with worked
examples. Read it rather than re-deriving the API from the tree.

## Rules that bite

- **Never hand-edit `gen/`, and never rewrite the module path with `sed`.** The
  file descriptors embed length-prefixed package strings; a text rewrite
  corrupts them and panics at `init()`. Regenerate (skill below).
- **Every generated type reachable from a facade needs an alias in that
  package's `types.go`** — and a generated enum needs its constants
  re-exported too, because an alias carries the type and not the constants.
  `internal/apiboundary` enforces this by walking types, not text.
- **`settings` imports no generated schema.** That is what makes it reusable
  byte-for-byte across products. Product-specific fields belong in the product
  proto and its own field catalog, never here.
- **`workcontext` fails closed.** No keys, an unreachable JWKS, or an unknown
  `kid` all reject the request. Replay is deliberately *not* enforced — a
  captured token is accepted until expiry, whatever its `replay_policy`; a
  callee needing single-use reads `GetReplayPolicy()` / `GetNonce()` and checks
  its own store. That is a documented limit, not an oversight to route around.
- **The release version tracks the saas-starter module version**, and
  `SOURCE.txt` records the proto ref `gen/` came from. Both move together with
  a regeneration, or the tag claims a contract it does not carry.

## Procedures

Step-by-step walks live in `.claude/skills/`, loaded on demand rather than
carried here:

- `api-boundary-triage` — `internal/apiboundary` is failing, and you need to
  tell the real fix from the several changes that would only make it green.
- `refresh-generated-api` — moving `gen/` to a newer proto ref, and the four
  other things that move with it.

## Workflow

- Branch and PR; never commit to `main`. Conventional Commits for the title.
- Keep this file under ~150 lines (hard cap 200). Push depth into
  `.claude/skills/`, into a nested `AGENTS.md` beside what it describes, or
  into `README.md` where it serves consumers too.
- `internal/agentcontext` holds that budget and each skill's frontmatter
  contract. It runs under `go test ./...`, so the walk above enforces it.
- `CLAUDE.md` is a pointer to this file. Keep one canonical source.
- Treat this file as code: the PR that changes a process updates it.
