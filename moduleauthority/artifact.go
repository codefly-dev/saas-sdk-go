package moduleauthority

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
	codefly "github.com/codefly-dev/sdk-go/workcontext"
)

var ErrInvalidArtifact = errors.New("module authority: invalid executable artifact request or receipt")
var artifactDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var artifactUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ArtifactContract is an exact dependency under an operator-installed ceiling.
type ArtifactContract struct {
	Kind   string
	Name   string
	Digest string
}

// ArtifactIdentity contains the complete canonical identity exported by the
// module's compiler. Subject contains references, never authored content or
// credentials. Schema owns canonical bytes and ordering; the SDK does not
// reinterpret the module's compiler digest or round-trip its JSON numbers.
type ArtifactIdentity struct {
	Schema           string
	Source           string
	Subject          []byte
	Contracts        []ArtifactContract
	ExpectedRevision int64
}

// ArtifactRequest addresses an installed policy. The host derives the tenant
// and caller from the signed Parent; no caller-chosen principal appears here.
type ArtifactRequest struct {
	Parent         codefly.WorkContextToken
	InstallationID string
	PolicyID       string
	Identity       ArtifactIdentity
}

// ArtifactAuthorization is a host-owned exact consent Ref, not a credential
// for executing a tool or reading an artifact. Approval changes no active pointer.
type ArtifactAuthorization struct {
	QualifiedName    string
	Digest           string
	ExpectedRevision int64
}

// ArtifactRevocation addresses a retained approval even when its old policy or
// installation no longer runs. The host checks the original activation scope.
type ArtifactRevocation struct {
	Parent         codefly.WorkContextToken
	InstallationID string
	ApprovalID     string
}

func artifactRequest(req ArtifactRequest) (*v1.ModuleExecutableArtifactRequest, error) {
	i := req.Identity
	if req.Parent.Encoded() == "" || !artifactUUID.MatchString(req.InstallationID) || req.PolicyID == "" || len(req.PolicyID) > 128 || i.Schema == "" || len(i.Schema) > 128 || i.Source == "" || len(i.Source) > 512 || i.ExpectedRevision < 0 || len(i.Subject) == 0 || len(i.Subject) > 65536 || len(i.Contracts) > 256 {
		return nil, ErrInvalidArtifact
	}
	contracts := make([]*v1.ExecutableArtifactContract, 0, len(i.Contracts))
	for _, c := range i.Contracts {
		if c.Kind == "" || len(c.Kind) > 64 || c.Name == "" || len(c.Name) > 512 || !artifactDigest.MatchString(c.Digest) {
			return nil, ErrInvalidArtifact
		}
		contracts = append(contracts, &v1.ExecutableArtifactContract{Kind: c.Kind, Name: c.Name, Digest: c.Digest})
	}
	return &v1.ModuleExecutableArtifactRequest{ParentWorkContextToken: req.Parent.Encoded(), InstallationId: req.InstallationID, PolicyId: req.PolicyID, Identity: &v1.ExecutableArtifactIdentity{Schema: i.Schema, Source: i.Source, Subject: append([]byte{}, i.Subject...), Contracts: contracts, ExpectedRevision: i.ExpectedRevision}}, nil
}
func artifactReceipt(out *v1.ModuleExecutableArtifactResponse, expected *int64) (ArtifactAuthorization, error) {
	const prefix = "host/approved-artifacts/"
	if out == nil || out.SchemaVersion != "host.executable-artifact/v1" || len(out.QualifiedName) <= len(prefix) || out.QualifiedName[:len(prefix)] != prefix || !artifactUUID.MatchString(out.QualifiedName[len(prefix):]) || !artifactDigest.MatchString(out.Digest) || out.ExpectedRevision < 0 || (expected != nil && out.ExpectedRevision != *expected) {
		return ArtifactAuthorization{}, ErrInvalidArtifact
	}
	return ArtifactAuthorization{QualifiedName: out.QualifiedName, Digest: out.Digest, ExpectedRevision: out.ExpectedRevision}, nil
}

// ApproveExecutableArtifact records explicit present-administrator consent.
// Never invoke it as a fallback when AuthorizeExecutableArtifact refuses.
func (c *Client) ApproveExecutableArtifact(ctx context.Context, req ArtifactRequest) (ArtifactAuthorization, error) {
	wire, err := artifactRequest(req)
	if err != nil {
		return ArtifactAuthorization{}, err
	}
	out, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.ApproveExecutableArtifact, wire)
	if err != nil {
		return ArtifactAuthorization{}, fmt.Errorf("module authority: approve executable artifact: %w", err)
	}
	return artifactReceipt(out, &req.Identity.ExpectedRevision)
}

// AuthorizeExecutableArtifact reads existing exact consent and rechecks live
// authority/revocation. It never creates approval or changes activation.
func (c *Client) AuthorizeExecutableArtifact(ctx context.Context, req ArtifactRequest) (ArtifactAuthorization, error) {
	wire, err := artifactRequest(req)
	if err != nil {
		return ArtifactAuthorization{}, err
	}
	out, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.AuthorizeExecutableArtifact, wire)
	if err != nil {
		return ArtifactAuthorization{}, fmt.Errorf("module authority: authorize executable artifact: %w", err)
	}
	return artifactReceipt(out, &req.Identity.ExpectedRevision)
}

// RevokeExecutableArtifact permanently revokes one retained consent. Replaying
// its prior approval request cannot revive that exact identity.
func (c *Client) RevokeExecutableArtifact(ctx context.Context, req ArtifactRevocation) (ArtifactAuthorization, error) {
	if req.Parent.Encoded() == "" || !artifactUUID.MatchString(req.InstallationID) || !artifactUUID.MatchString(req.ApprovalID) {
		return ArtifactAuthorization{}, ErrInvalidArtifact
	}
	out, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.RevokeExecutableArtifact, &v1.ModuleRevokeExecutableArtifactRequest{ParentWorkContextToken: req.Parent.Encoded(), InstallationId: req.InstallationID, ApprovalId: req.ApprovalID})
	if err != nil {
		return ArtifactAuthorization{}, fmt.Errorf("module authority: revoke executable artifact: %w", err)
	}
	receipt, err := artifactReceipt(out, nil)
	if err == nil && receipt.QualifiedName != "host/approved-artifacts/"+req.ApprovalID {
		return ArtifactAuthorization{}, ErrInvalidArtifact
	}
	return receipt, err
}
