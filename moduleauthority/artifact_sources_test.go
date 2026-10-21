package moduleauthority

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestArtifactSourcesPreservesSelectorsAndIndependentCredentials(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	req := ExecutableArtifactSourcesRequest{Parent: sealedToken("parent.token"), InstallationID: exampleSource, PolicyID: "example-policy"}
	out, err := client.ListExecutableArtifactSources(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if out.Installation != (CurrentInstallation{InstallationID: exampleSource, TenantID: testTenant, TargetID: testOwner, BindingID: testBindingID}) || out.PolicyID != req.PolicyID || !reflect.DeepEqual(out.Sources, []string{"example/first", "example/second"}) {
		t.Fatal("lost association or complete set", out)
	}
	calls := h.authority.snapshot()
	if len(calls) != 1 {
		t.Fatal(calls)
	}
	call := calls[0]
	if call.artifactSources == nil || call.artifactSources.InstallationId != req.InstallationID || call.artifactSources.PolicyId != req.PolicyID || call.artifactSources.ParentWorkContextToken != req.Parent || call.artifactSources.ProtoReflect().Descriptor().Fields().Len() != 3 {
		t.Fatal("selector or parent changed")
	}
	if len(call.authorization) != 0 || len(call.workContext) != 1 || call.workContext[0] != sealedToken("module.token1") || len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken {
		t.Fatal("independent credentials not preserved")
	}
	h.assertNoStrayPaths(t)
}

func TestArtifactSourcesPreservesZeroOneAndManyWithoutChoosing(t *testing.T) {
	for _, sources := range [][]string{{}, {"opaque:one"}, {"second", "first"}} {
		t.Run(strings.Join(sources, ","), func(t *testing.T) {
			h := newHost(t)
			h.authority.artifactSourcesResponse = sourceAnswer()
			h.authority.artifactSourcesResponse.Sources = sources
			out, err := h.client(t, moduleCredentials).ListExecutableArtifactSources(t.Context(), sourceRequest())
			if err != nil || !reflect.DeepEqual(out.Sources, sources) {
				t.Fatal(out, err)
			}
		})
	}
}

func sourceRequest() ExecutableArtifactSourcesRequest {
	return ExecutableArtifactSourcesRequest{Parent: sealedToken("parent.token"), InstallationID: exampleSource, PolicyID: "example-policy"}
}

func sourceAnswer() *v1.ModuleExecutableArtifactSourcesResponse {
	return &v1.ModuleExecutableArtifactSourcesResponse{
		Installation: &v1.ModuleCurrentInstallationResponse{InstallationId: exampleSource, TenantId: testTenant, TargetId: testOwner, BindingId: testBindingID},
		PolicyId:     "example-policy", Sources: []string{"example/first"},
	}
}

func TestArtifactSourcesRejectsMalformedAssociationsAndSets(t *testing.T) {
	cases := map[string]func(*v1.ModuleExecutableArtifactSourcesResponse){
		"other policy":         func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.PolicyId = "other" },
		"missing installation": func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Installation = nil },
		"other installation":   func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Installation.InstallationId = testOwner },
		"invalid tenant":       func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Installation.TenantId = "invalid" },
		"invalid target":       func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Installation.TargetId = "" },
		"empty binding":        func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Installation.BindingId = " " },
		"duplicate source":     func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Sources = []string{"one", "one"} },
		"empty source":         func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Sources = []string{""} },
		"wildcard source":      func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Sources = []string{"all/*"} },
		"control source":       func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Sources = []string{"one\n"} },
		"long source":          func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Sources = []string{strings.Repeat("a", 513)} },
		"too many sources":     func(r *v1.ModuleExecutableArtifactSourcesResponse) { r.Sources = make([]string, 129) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			h.authority.artifactSourcesResponse = sourceAnswer()
			mutate(h.authority.artifactSourcesResponse)
			_, err := h.client(t, moduleCredentials).ListExecutableArtifactSources(t.Context(), sourceRequest())
			if !errors.Is(err, ErrInvalidArtifactSources) {
				t.Fatal(err)
			}
		})
	}
}

func TestArtifactSourcesRefusalsDoNotFallbackOrApprove(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	for _, code := range []codes.Code{codes.PermissionDenied, codes.Unavailable} {
		h.authority.artifactSourcesRefusal = status.Error(code, "owner verdict")
		before := len(h.authority.snapshot())
		_, err := client.ListExecutableArtifactSources(t.Context(), sourceRequest())
		if connect.CodeOf(err) != connect.Code(code) || len(h.authority.snapshot()) != before+1 {
			t.Fatal("refusal lost or retried", err)
		}
	}
	before := len(h.authority.snapshot())
	for _, mutate := range []func(*ExecutableArtifactSourcesRequest){
		func(r *ExecutableArtifactSourcesRequest) { r.Parent = "not-sealed" },
		func(r *ExecutableArtifactSourcesRequest) { r.InstallationID = "invalid" },
		func(r *ExecutableArtifactSourcesRequest) { r.PolicyID = "" },
		func(r *ExecutableArtifactSourcesRequest) { r.PolicyID = strings.Repeat("a", 129) },
	} {
		req := sourceRequest()
		mutate(&req)
		if _, err := client.ListExecutableArtifactSources(t.Context(), req); !errors.Is(err, ErrInvalidArtifactSources) {
			t.Fatal(err)
		}
	}
	if len(h.authority.snapshot()) != before {
		t.Fatal("invalid request reached host")
	}
	h.assertNoStrayPaths(t)
}
