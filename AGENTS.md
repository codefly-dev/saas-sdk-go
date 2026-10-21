# Working in codefly-dev/saas-sdk-go

`github.com/codefly-dev/saas-sdk-go` (Go 1.27) is the **published SDK** that
solutions and modules depend on instead of regenerating and vendoring their own
copies. Four surfaces: the generated accounts bindings behind hand-written
facades (`accounts/`, `datasource/`), the schema-agnostic typed-settings runtime
and its catalog renderers (`settings/`), and a composed module's own authority
(`moduleauthority/`).

It does **not** own:

- the accounts **proto**. That is `codefly-dev/module-saas-starter`
  (`module/services/accounts/proto`); `gen/` here is output, never source.
- the Work Context **wire format** or its **verification**. The format and the
  structural reader are `codefly-dev/core` (`core/workcontext`); the callee's
  verifier is `codefly-dev/sdk-go/workcontext`, used directly. This SDK
  carries Work Contexts as opaque strings and has no callee side.
- the resource and configuration model (`codefly-dev/core`), the gateway
  (`codefly-dev/solution-runtime-go`), or any product's settings schema.

A change that belongs to one of those gets made there and consumed here. This
is a library: a defect is always observed somewhere downstream, so the pull
toward absorbing it locally is constant and the rules below exist against it.

## How to behave

Fleet standard — [handbook#68](https://github.com/obin-ai/handbook/issues/68).

- **Prove reuse before you build.** The default answer is that the platform
  already has it and your change fills a gap or adds a helper on it, so the PR
  body names what you reused and what you deliberately did not. A second
  mechanism for a job one seam already does is refused however clean it is. Two
  tells you are reinventing: what you are writing re-answers a question an
  existing seam answered, and your reason for its placement is that a
  convenient loop was already there rather than that the responsibility belongs
  to it.
- **Build the simplest thing that works; size is the signal.** A coding task's
  budget is about **500 hand-written lines** — generated output does not count,
  tests count apart — and past **2,000** the change has become a second system,
  so **stop and ask the user** instead of finishing it. Two tells, checkable
  before the code exists: you cannot name the one responsibility the change
  adds, and it is growing to make the end goal pass rather than to put a
  behaviour where it belongs. Wanting it to work licenses no means to get
  there.
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
  `BaseURL()` off it rather than holding a URL. `moduleauthority` takes its
  gateway and authority seams from the consumer and defaults neither address:
  a zero `Authority` builds a mint-only client that fails with
  `ErrNoAuthority`, never one that dials an address nobody configured. The one
  value it does resolve is a credential source the consumer opts into, the
  projected token's path: `WorkloadTokenFile()` reads it from the variable a
  native run injects (`CODEFLY__WORKLOAD_TOKEN_FILE`) and otherwise falls back
  to the mount a cell's render declares, and a consumer whose path comes from
  elsewhere passes `ProjectedTokenFile(path)`, which has no default. Typing one
  of those in encodes something true
  on one machine for ten minutes, and it fails quietly — a runtime missing a
  credential can skip registration *silently*, so the service boots, serves,
  and is simply absent.
- **Diagnose, do not pattern-match.** "It started working when I set X" is not
  a diagnosis — set X back and confirm it breaks. Do not trust an error message
  before checking its claim: a `gen/` panic at `init()` reporting a malformed
  descriptor has meant a text-rewritten module path, not a bad protobuf
  release.
- **Say what you did not verify.** Unverified is not the same as working. CI
  (below) runs the build walk, the `gen/` drift check and the host-gateway
  mint, nothing more — if you could not exercise something else, the PR says so.

## Build and test

`.github/workflows/ci.yml` runs this walk on every pull request and push to
`main`, for every `go.mod` outside `testdata/`. Run it locally first:

```bash
go build ./...
go vet ./...
go test ./...                    # ~0.4s; the real gate lives here
go test -race ./...
gofmt -l . | grep -v '^gen/'     # expect no output
```

`go test ./...` is load-bearing beyond the unit tests: `internal/apiboundary`
type-checks the entire public surface against the boundary rule below, and
`internal/agentcontext` checks these context files. Keep `golang.org/x/tools`
compatible with the active Go toolchain: its importer must understand Go's
export data for the boundary gate to run. There is no lint config —
`golangci-lint` being installed on your machine does not make it a gate here.

The same workflow's `gen-drift` job regenerates `gen/` from the ref in
`SOURCE.txt` with this repo's `buf.gen.yaml` (buf itself pinned in the
workflow) and fails on any difference — the reproduction check from the
`refresh-generated-api` skill, run for you. Its `host-gateway-conformance` job
mints through the host's *real* gateway handlers at that same ref
(`conformance/hostgateway/run.sh <host checkout>`; see the `AGENTS.md` there).

## Where things live

| Path | Owns |
| --- | --- |
| `accounts/` | gateway-bound facade over the accounts services (`Audit()`, `Datasources()`: the raw contract); `types.go` holds its alias re-exports |
| `datasource/` | the ergonomic, effect-safe facade over `DatasourceService` |
| `settings/` | presence-aware `Field[M, T]` runtime; `JSONCodec` is the only typed↔ProtoJSON boundary |
| `settings/catalog/` | Go / TypeScript / proto catalog renderers that `module-compose` calls |
| `moduleauthority/` | a module's own authority: gateway broker mints; direct Accounts authority or explicitly selected gateway authority transport. Work Contexts are opaque `string`s, shape-checked only by `core/workcontext.Decode`. `verifier_sources.go` serves core's three live-authority sources to a callee's verifier without verifying anything itself |
| `gen/` | generated output. Regenerated, never edited |
| `conformance/hostgateway/` | `moduleauthority` minting through the host's real gateway at the `SOURCE.txt` ref |
| `internal/apiboundary` | the public-API boundary gate, with fixtures that prove it still looks |
| `internal/connectresponse` | the one HTTP client both `DatasourceService` facades read responses through |
| `internal/agentcontext` | the budget and frontmatter contract for this file and the skills |

`README.md` covers what each surface does for a *consumer*, with worked
examples. Read it rather than re-deriving the API from the tree.

## Rules that bite

- **Never hand-edit `gen/`, and never rewrite the module path with `sed`.** The
  file descriptors embed length-prefixed package strings; a text rewrite
  corrupts them and panics at `init()`. Regenerate (skill below).
  Generate the host-owned `saas/` tree; Core's vendored Runnable imports resolve
  to Core's Go dependency, never a second descriptor copy in this SDK.
- **Every generated type reachable from a facade needs an alias in that
  package's `types.go`** — and a generated enum needs its constants
  re-exported too, because an alias carries the type and not the constants.
  `internal/apiboundary` enforces this by walking types, not text.
- **`settings` imports no generated schema.** That is what makes it reusable
  byte-for-byte across products. Product-specific fields belong in the product
  proto and its own field catalog, never here.
- **`moduleauthority` holds Work Contexts; it never verifies one.** A token is
  carried only if `core/workcontext.Decode` reads it as sealed — never a
  hand-written parser, and never a decoded claim treated as a finding.
  Verifying belongs to the receiver (`sdk-go/workcontext`); a callee-side
  verifier does not come back here.
  **A SOURCE is not a verifier.** `WorkContextSources` implements core's
  `SealSource`, `RevisionSource` and `GrantSource` over the host's
  `ResolveWorkContextAuthority`, and that belongs here: it holds no key, reads
  no signature and decides nothing about a capability — it is a client of the
  ISSUER's live state, which is this SDK's whole job. The rule above is about
  the decision, and the decision stays in `sdk-go/workcontext`. A source may
  read a claim off a capability to NARROW what it asks (the owner it questions
  is the capability's own) and never to settle anything: the host checks the
  same claim against the same capability, so a misreading refuses.
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
