#!/usr/bin/env bash
# Mint through the host's REAL auth-gateway handlers with this SDK's real
# moduleauthority client. See AGENTS.md beside this file.
#
#   conformance/hostgateway/run.sh <module-saas-starter checkout> [go test flags]
#
# The checkout must be at the ref SOURCE.txt records, and is never modified:
# the test file is added to the gateway package with `go test -overlay`, and
# the SDK is required (replaced by this working tree) in a copy of the
# gateway's go.mod passed with `-modfile`. Both live in a temporary directory.
set -euo pipefail

if [ $# -lt 1 ]; then
	echo "usage: $0 <module-saas-starter checkout> [go test flags]" >&2
	exit 2
fi

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
sdk=$(cd "$here/../.." && pwd)
host=$(cd "$1" && pwd)
shift

ref=$(grep -m1 -oE 'codefly-dev/module-saas-starter@(v[0-9]+\.[0-9]+\.[0-9]+|[0-9a-f]{40})$' "$sdk/SOURCE.txt" || true)
ref=${ref#*@}
gateway=$(grep -m1 -oE 'auth gateway: [^)]+' "$sdk/SOURCE.txt" | sed 's/^auth gateway: //' || true)
if [ -z "$ref" ] || [ -z "$gateway" ]; then
	echo "SOURCE.txt records no module-saas-starter ref or no '(auth gateway: <path>)' line" >&2
	exit 1
fi

# The contract under test is the one gen/ was generated from, and no other.
want=$(git -C "$host" rev-parse --verify -q "$ref^{commit}" || true)
have=$(git -C "$host" rev-parse HEAD)
if [ -z "$want" ] || [ "$want" != "$have" ]; then
	echo "$host is at $have, not at $ref (SOURCE.txt); check out that ref" >&2
	exit 1
fi
unmodified() {
	if [ -n "$(git -C "$host" status --porcelain)" ]; then
		echo "$host has local changes; the host must be an unmodified checkout" >&2
		git -C "$host" status --short >&2
		exit 1
	fi
}
unmodified

pkg="$host/$gateway"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp "$pkg/go.mod" "$work/go.mod"
cp "$pkg/go.sum" "$work/go.sum"
go mod edit \
	-require=github.com/codefly-dev/saas-sdk-go@v0.0.0 \
	-replace=github.com/codefly-dev/saas-sdk-go="$sdk" \
	"$work/go.mod"
cat >"$work/overlay.json" <<EOF
{"Replace": {"$pkg/zz_saas_sdk_go_conformance_test.go": "$here/testdata/sdk_conformance_test.go"}}
EOF

export GOWORK=off GOFLAGS=
# The host's auth-gateway/pkg/gen/saas/accounts/v1 and this SDK's
# gen/saas/accounts/v1 register the same proto files, and this one test binary
# links both. Nothing here resolves a message through the global registry (the
# broker is JSON over HTTP; the accounts fake decodes with the host's types),
# so the duplicate registration is not fatal. (`warn` prints some 120 lines
# ahead of any failure.)
export GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore

cd "$pkg"
# Requiring the SDK can raise versions the host pins; the test runs with
# these, so say which.
moved=$(diff <(go list -m all) <(go list -mod=mod -modfile="$work/go.mod" -m all) | grep '^>' || true)
if [ -n "$moved" ]; then
	echo "module versions in the conformance build that differ from the host's own:"
	echo "$moved"
fi
go test -mod=mod -modfile="$work/go.mod" -overlay="$work/overlay.json" \
	-run '^TestSDKConformance' -count=1 "$@" .

unmodified
