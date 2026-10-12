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
#
# It fails unless every conformance test below was listed, ran and passed; and
# unless the build gives the gateway exactly the grpc, protobuf and
# sdk-go/workcontext it ships with (or a byte-identical module tree).
set -euo pipefail

# Every conformance test, by name. A test added to the file is added here: a
# rename, a filter that matches nothing, or a skip is a failure, not a pass.
expected=(
	TestSDKConformanceFakeGatewayStillFollowsThisOne
	TestSDKConformanceNoOrganization
	TestSDKConformanceRefusals
	TestSDKConformanceRemovedAndMalformedCredentials
	TestSDKConformanceRotatedProjectedFile
	TestSDKConformanceThreeMints
	TestSDKConformanceTokenBound
	TestSDKConformanceWrongInternalToken
)

# The modules whose version changes what the gateway under test does. Any
# other module the SDK moves is reported, not gated.
guarded=(
	google.golang.org/grpc
	google.golang.org/protobuf
	github.com/codefly-dev/sdk-go/workcontext
)

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
cat >"$work/overlay.json" <<EOF
{"Replace": {"$pkg/zz_saas_sdk_go_conformance_test.go": "$here/testdata/sdk_conformance_test.go"}}
EOF

export GOWORK=off GOFLAGS=
# The host's auth-gateway/pkg/gen/saas/accounts/v1 and this SDK's
# gen/saas/accounts/v1 register the same proto files, and this one test binary
# links both. Nothing here resolves a message through the global registry (the
# broker is JSON over HTTP; the accounts fake decodes with the host's types),
# so the duplicate registration is not fatal. (`warn` prints some 120 lines
# ahead of any failure.) gen-drift proves both copies come from one schema.
export GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore

cd "$pkg"
modfile=(-modfile="$work/go.mod")
# Add exactly one dependency, the SDK's moduleauthority, with the requirements
# its own go.mod declares. `go get` would look up at @latest any import of
# moduleauthority that go.mod does not declare, so first require that there is
# none: the SDK's own -mod=readonly resolution of the package must succeed.
# Everything after `go get` runs -mod=readonly too: an import no required
# module provides is an error, never a lookup at @latest.
if ! (cd "$sdk" && go list -mod=readonly -deps ./moduleauthority >/dev/null); then
	echo "moduleauthority imports a package the SDK's go.mod does not declare;" >&2
	echo "go get would resolve it at @latest. Require it in go.mod first." >&2
	exit 1
fi
go mod edit -replace=github.com/codefly-dev/saas-sdk-go="$sdk" "$work/go.mod"
go get "${modfile[@]}" github.com/codefly-dev/saas-sdk-go/moduleauthority@v0.0.0
build=(-mod=readonly "${modfile[@]}" -overlay="$work/overlay.json")

version() { awk -v m="$1" '$1 == m { print $2 }'; }
host_modules=$(go list -mod=readonly -m all)
build_modules=$(go list "${build[@]}" -m all)
mkdir "$work/download"
tree() {
	(cd "$work/download" && go mod download -json "$1@$2" | sed -n 's/^	"Dir": "\(.*\)",$/\1/p')
}
for mod in "${guarded[@]}"; do
	from=$(version "$mod" <<<"$host_modules")
	to=$(version "$mod" <<<"$build_modules")
	if [ "$from" = "$to" ]; then
		continue
	fi
	if [ -n "$from" ] && [ -n "$to" ] && diff -r -q "$(tree "$mod" "$from")" "$(tree "$mod" "$to")" >/dev/null; then
		echo "note: $mod $from -> $to in the conformance build; the two module trees are byte-identical"
		continue
	fi
	echo "the conformance build resolves $mod ${to:-(absent)}, the host pins ${from:-(absent)}," >&2
	echo "and their sources differ: the gateway under test would not be the one the host ships" >&2
	exit 1
done
moved=$(diff <(echo "$host_modules") <(echo "$build_modules") | grep '^>' || true)
if [ -n "$moved" ]; then
	echo "warning: module versions in the conformance build that differ from the host's own:"
	echo "$moved"
fi

listed=$(go test "${build[@]}" -list '^TestSDKConformance' . | { grep '^Test' || true; } | sort)
if [ "$listed" != "$(printf '%s\n' "${expected[@]}" | sort)" ]; then
	echo "the conformance tests in the file are not the ones run.sh expects:" >&2
	diff <(printf '%s\n' "${expected[@]}" | sort) <(echo "$listed") >&2 || true
	exit 1
fi

status=0
go test "${build[@]}" -run '^TestSDKConformance' -count=1 -v "$@" . | tee "$work/test.log" || status=$?
unmodified
if [ "$status" -ne 0 ]; then
	exit "$status"
fi
for name in "${expected[@]}"; do
	if ! grep -q "^--- PASS: $name (" "$work/test.log"; then
		echo "$name did not run and pass" >&2
		exit 1
	fi
done
if grep -q -- '--- SKIP:' "$work/test.log"; then
	echo "a conformance test was skipped:" >&2
	grep -- '--- SKIP:' "$work/test.log" >&2
	exit 1
fi
echo "conformance: ${#expected[@]} tests ran and passed against module-saas-starter@$ref"
