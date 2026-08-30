package catalog

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// A protobuf field number is a positive int up to 2^29-1, excluding the
// 19000-19999 range the wire format reserves. A contribution outside this
// range renders into a proto that protoc rejects.
const (
	minFieldNumber      = 1
	maxFieldNumber      = 536870911
	reservedFieldNumMin = 19000
	reservedFieldNumMax = 19999
)

var (
	namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	messagePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(?:\.[A-Za-z][A-Za-z0-9_]*)+$`)
	fieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Validate reports the first contribution whose identifiers or field number
// would render into invalid Go, TypeScript, or proto output. It is the
// structural guardrail that module-compose used to apply before the renderers
// were internal; a consumer calling the renderers directly must Validate first.
//
// Validate covers only per-contribution structural validity, which is
// schema-agnostic and universal. Cross-contribution and policy checks
// (duplicate namespaces or field numbers, field-range allocation and
// collision, reserved-namespace lists) depend on the caller's module
// configuration and remain the caller's responsibility.
func Validate(contributions []Contribution) error {
	for _, contribution := range contributions {
		if err := contribution.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate reports whether the contribution's identifiers and field number are
// structurally valid to render.
func (c Contribution) Validate() error {
	if !namespacePattern.MatchString(c.Namespace) {
		return fmt.Errorf("settings contribution namespace %q is not a valid lower-case identifier", c.Namespace)
	}
	if !messagePattern.MatchString(c.Message) {
		return fmt.Errorf("settings contribution %q message %q is not a fully qualified proto message name", c.Namespace, c.Message)
	}
	if !safeRelativeImport(c.ProtoImport) {
		return fmt.Errorf("settings contribution %q proto import %q must be a relative .proto path", c.Namespace, c.ProtoImport)
	}
	if !fieldNamePattern.MatchString(c.FieldName) {
		return fmt.Errorf("settings contribution %q field name %q is not a valid lower-case proto field name", c.Namespace, c.FieldName)
	}
	if c.FieldNumber < minFieldNumber || c.FieldNumber > maxFieldNumber ||
		(c.FieldNumber >= reservedFieldNumMin && c.FieldNumber <= reservedFieldNumMax) {
		return fmt.Errorf("settings contribution %q field number %d is not a valid proto field number", c.Namespace, c.FieldNumber)
	}
	return nil
}

// safeRelativeImport reports whether p is a slash-separated relative path that
// stays within the import root and names a .proto file. Proto imports are
// always slash-separated, so this is evaluated with slash semantics rather than
// the host filepath rules.
func safeRelativeImport(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
		return false
	}
	if path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	return path.Ext(p) == ".proto"
}
