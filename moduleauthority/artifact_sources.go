package moduleauthority

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

var ErrInvalidArtifactSources = errors.New("module authority: invalid executable artifact sources request or response")

// ExecutableArtifactSourcesRequest selects one effective policy and current
// installation. Parent remains independent of the calling module's credential.
type ExecutableArtifactSourcesRequest struct {
	Parent         string
	InstallationID string
	PolicyID       string
}

// ExecutableArtifactSources is a complete policy-scoped source set for one
// current installation. It grants no publication, activation or execution.
// Empty is a valid observation; no element is preferred or exclusively owned.
type ExecutableArtifactSources struct {
	Installation CurrentInstallation
	PolicyID     string
	Sources      []string
}

// ListExecutableArtifactSources reads member-visible host metadata. Callers must
// check Installation.TenantID against their independently verified request tenant
// and explicitly select or check a source. Later actions recheck their authority.
func (c *Client) ListExecutableArtifactSources(ctx context.Context, req ExecutableArtifactSourcesRequest) (ExecutableArtifactSources, error) {
	if !sealed(req.Parent) || !artifactUUID.MatchString(req.InstallationID) || !artifactPolicyValue(req.PolicyID, 128) {
		return ExecutableArtifactSources{}, ErrInvalidArtifactSources
	}
	out, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.ListExecutableArtifactSources, &v1.ModuleExecutableArtifactSourcesRequest{
		ParentWorkContextToken: req.Parent, InstallationId: req.InstallationID, PolicyId: req.PolicyID,
	})
	if err != nil {
		return ExecutableArtifactSources{}, fmt.Errorf("module authority: executable artifact sources: %w", err)
	}
	if out == nil || out.PolicyId != req.PolicyID || len(out.Sources) > 128 {
		return ExecutableArtifactSources{}, ErrInvalidArtifactSources
	}
	installation, err := currentInstallation(out.Installation, req.InstallationID)
	if err != nil {
		return ExecutableArtifactSources{}, ErrInvalidArtifactSources
	}
	seen := make(map[string]bool, len(out.Sources))
	for _, source := range out.Sources {
		if !artifactPolicyValue(source, 512) || seen[source] {
			return ExecutableArtifactSources{}, ErrInvalidArtifactSources
		}
		seen[source] = true
	}
	return ExecutableArtifactSources{Installation: installation, PolicyID: out.PolicyId, Sources: append([]string{}, out.Sources...)}, nil
}

// These are the host policy's opaque-value bounds, not a consumer namespace
// grammar. Consumer syntax and reserved identifiers remain the consumer's job.
func artifactPolicyValue(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) || strings.Contains(value, "*") {
		return false
	}
	for _, r := range value {
		if r <= 32 || r == 127 {
			return false
		}
	}
	return true
}
