# Host-gateway conformance

`moduleauthority` minting through the host's **real** auth-gateway handlers —
module-saas-starter's `module/services/auth-gateway/code` at the ref
`SOURCE.txt` records — rather than only through `moduleauthority`'s
hand-written fake. The fake once still accepted `X-Codefly-Module-Secret`
after the host stopped reading it, and every test here stayed green.

```bash
git clone --depth 1 --branch "$(grep -m1 -oE 'v[0-9]+\.[0-9]+\.[0-9]+$' SOURCE.txt)" \
  https://github.com/codefly-dev/module-saas-starter.git /tmp/saas-host
conformance/hostgateway/run.sh /tmp/saas-host -race -v
```

CI's `host-gateway-conformance` job runs the same script on the checkout the
`host-source` action makes (the one `gen-drift` uses).

## How it is wired, and why

- `testdata/sdk_conformance_test.go` is this repo's code but `package main` of
  the host's gateway: the gateway is a `main` package and its harness
  (`newGatewayHarness`) is a test helper, so nothing of it can be imported.
  `run.sh` adds the file to that package with `go test -overlay` and requires
  this working tree (by `replace`) in a temporary copy of the gateway's
  `go.mod` passed with `-modfile`. The host checkout must be at the recorded
  ref and is checked to be unmodified before and after. Nothing of the host is
  copied here.
- The file binds to four unexported host names, listed at its top. A host
  rename breaks the compile — red, never a silent pass. The fix for that
  coupling is an exported harness in the host, not a copy here.
- Only accounts' three `Mint*` RPCs are faked, on a real gRPC listener, as the
  host's own gateway tests fake them: the real mints need a Kubernetes
  TokenReview and a database. What accounts *decides* is therefore not under
  test — only what the gateway forwards to it and how it relays each answer.
- Requiring the SDK raises some module versions the host pins (`run.sh`
  prints which); the gateway is compiled with those.
- The host's and this SDK's `gen/saas/accounts/v1` register the same proto
  files in one binary, so the script sets
  `GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore`. Nothing under test resolves
  through the global registry.

## What it holds

The three mints accepted, and exactly `prefix`, `service_account_token`,
`organization_id`, `binding` and `source_id` reaching accounts; each accounts
refusal → gateway status → SDK sentinel; the legacy header, a missing,
duplicate, non-Bearer or empty bearer refused before accounts; the 64 KiB
token bound; and the three gateway answers the fake copies (D1, D3, D4 in
`moduleauthority/host_fake_test.go`).

Red here means the host's contract moved, or the client broke it. Fix the
client (or realign the fake), never the expectation, unless the host's own
change says the contract changed — and then the PR names that host change.
