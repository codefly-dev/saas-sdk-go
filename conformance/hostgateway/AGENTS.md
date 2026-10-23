# Host-gateway conformance

`moduleauthority` minting through the host's **real** auth-gateway handlers —
module-saas-starter's `module/services/auth-gateway/code` at the ref
`SOURCE.txt` records — rather than only through `moduleauthority`'s
hand-written fake. The fake once still accepted `X-Codefly-Module-Secret`
after the host stopped reading it, and every test here stayed green.

```bash
# SOURCE.txt records a release tag or a 40-hex commit; this fetches either.
ref=$(grep -m1 -oE 'module-saas-starter@(v[0-9]+\.[0-9]+\.[0-9]+|[0-9a-f]{40})$' SOURCE.txt)
ref=${ref#*@}
case $ref in v*) spec="refs/tags/$ref:refs/tags/$ref" ;; *) spec=$ref ;; esac
git init -q /tmp/saas-host
git -C /tmp/saas-host fetch -q --depth 1 https://github.com/codefly-dev/module-saas-starter.git "$spec"
git -C /tmp/saas-host checkout -q "$ref"
conformance/hostgateway/run.sh /tmp/saas-host -race
```

CI's `host-gateway-conformance` job runs the same script on the checkout the
`host-source` action makes (the one `gen-drift` uses).

The script fails, rather than passes, when:
- the tests in the file are not exactly the `expected` list at its top, or
  any of them did not run and pass, or one skipped. A test added to the file
  is added to that list too.
- the build gives the gateway a different `google.golang.org/grpc`,
  `google.golang.org/protobuf` or `sdk-go/workcontext` from the one the host
  pins. The one exception is a version whose module tree is byte-identical,
  checked on every run. The SDK no longer requires `sdk-go/workcontext`, so
  today the build keeps the host's own pin of it. Other modules the SDK moves,
  such as `core` and `golang.org/x/*`, are printed as warnings.
- `moduleauthority` imports a package the SDK's own `go.mod` does not declare,
  which the SDK's `go list -mod=readonly -deps ./moduleauthority` refuses before
  `go get` adds the SDK; or the test file imports a package no required module
  provides, since everything after `go get` runs with `-mod=readonly`. Between
  them, nothing is resolved at `@latest`.

## How it is wired, and why

- `testdata/sdk_conformance_test.go` is this repo's code but `package main` of
  the host's gateway: the gateway is a `main` package and its harness
  (`newGatewayHarness`) is a test helper, so nothing of it can be imported.
  `run.sh` adds the file to that package with `go test -overlay` and requires
  this working tree (by `replace`) in a temporary copy of the gateway's
  `go.mod` passed with `-modfile`. The host checkout must be at the recorded
  ref and is checked to be unmodified before and after. Nothing of the host is
  copied here.
- The file binds to five unexported host names, listed at its top. A host
  rename breaks the compile — red, never a silent pass. The fix for that
  coupling is an exported harness in the host, not a copy here.
- Only accounts' three `Mint*` RPCs are faked, on a real gRPC listener, as the
  host's own gateway tests fake them: the real mints need a Kubernetes
  TokenReview and a database. What accounts *decides* is therefore not under
  test — only what the gateway forwards to it and how it relays each answer.
  That includes the token's encoding: the fake answers core-format Work
  Contexts (signed with core's public fixture key) because the SDK carries
  nothing else, and the host's real accounts before module-saas-starter#1093
  mints the pre-cutover JSON payload — which
  `TestSDKConformancePreCutoverTokenRefused` holds the SDK refuses.
- Requiring the SDK raises some module versions the host pins. The gateway is
  compiled with those, so the three that change what it does are gated (see
  above), and the rest are printed.
- The host's and this SDK's `gen/saas/accounts/v1` register the same proto
  files in one binary, so the script sets
  `GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore`. Nothing under test resolves
  through the global registry.

## What it holds

The three mints accepted, and exactly `prefix`, `service_account_token`,
`organization_id`, `binding` and `source_id` reaching accounts; each accounts
refusal → gateway status → SDK sentinel; a pre-cutover token relayed and
refused as `ErrInvalidCapability`; the legacy header, a missing,
duplicate, non-Bearer or empty bearer refused before accounts; the 64 KiB
token bound; and the three gateway answers the fake copies (D1, D3, D4 in
`moduleauthority/host_fake_test.go`).

A work-context mint's `FailedPrecondition` maps to HTTP 412 (nothing to mint
against, installation pending among them), which the SDK reads as
`ErrNoHostStateToMintFor`. This differs from the source mint's 412
(`ErrDelegationMissing`).

Since host `af5cef9a` (module-saas-starter#1120, in v0.0.103) the three mints
are served on the gateway's workload endpoint alone and answer 404 on the
public one, so the harness serves the gateway through the host's own
`workloadListener`, as a native run's workload server does. A cell wraps that
listener in a waypoint-authenticated presenter check. The conformance suite
uses the host's own authenticated listener and presenter fixtures to check
missing, forged and duplicate assertions, forwarding of the trusted presenter,
and refusal on the public listener. The same commit dropped the internal token
from the work-context mint — that endpoint is its perimeter — and relays accounts'
`Unauthenticated` there as 401 (`ErrInvalidCredentials`), as it already did
`PermissionDenied`. The two operation mints still hold the internal token.

## When it goes red

First ask which kind of red it is. Two kinds look alike after a `SOURCE.txt`
move and need opposite fixes.

- **The test file does not compile** (`[setup failed]` / `build failed`,
  pointing into `zz_saas_sdk_go_conformance_test.go`). The host changed what
  the file is bound to:
  - one of the five unexported names;
  - `newGatewayHarness`'s results;
  - an `accountsv1` request or response field the fake accounts reads or
    answers with.

  Re-bind the file at the new ref. Edit `newRealGateway` and the `Get…`
  accessors to the host's new spelling, and change no assertion about status,
  header, body or sentinel. This is the likeliest red after a ref move, and it
  says nothing yet about the SDK.
- **It compiles and an assertion fails** (`--- FAIL`). The broker contract
  moved, or the client broke it. That covers a path, a header, a JSON field
  name, a status, or what reaches accounts. Fix the client, or realign the
  fake. Change an expectation only when the host's own change says the
  contract changed, and name that host commit in the PR.
- **`run.sh` fails before or after `go test`** on the ref, the test list, a
  guarded module or the host checkout's state. Its message names the cause.

A compile error that renames an accounts *request* field is both: re-bind the
accessor, then let the assertions say whether the SDK-facing wire changed.
