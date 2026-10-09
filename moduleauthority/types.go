package moduleauthority

import v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"

// Generated contract aliases used by the facade. They keep consumers on this
// package boundary when they need the complete response projection.
type (
	ModuleAuditEventTypeDeclaration = v1.ModuleAuditEventTypeDeclaration
	ModuleAuditFieldDeclaration     = v1.ModuleAuditFieldDeclaration
	ModuleAuditFieldKind            = v1.ModuleAuditFieldKind
	ModuleAuditEventVisibility      = v1.ModuleAuditEventVisibility

	ModuleMintWorkContextRequest                    = v1.ModuleMintWorkContextRequest
	ModuleMintWorkContextResponse                   = v1.ModuleMintWorkContextResponse
	ModuleMintOperationContextRequest               = v1.ModuleMintOperationContextRequest
	ModuleMintOperationContextResponse              = v1.ModuleMintOperationContextResponse
	ModuleExchangeDelegatedOperationAudienceRequest = v1.ModuleExchangeDelegatedOperationAudienceRequest
	ModuleMintSourceOperationContextResponse        = v1.ModuleMintSourceOperationContextResponse
	IssuedWorkContext                               = v1.IssuedWorkContext
)

const (
	ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_UNSPECIFIED             = v1.ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_UNSPECIFIED
	ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_STRING                  = v1.ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_STRING
	ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_UUID                    = v1.ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_UUID
	ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_INT                     = v1.ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_INT
	ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_NUMBER                  = v1.ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_NUMBER
	ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_BOOL                    = v1.ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_BOOL
	ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_ENUM                    = v1.ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_ENUM
	ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_STRING_ARRAY            = v1.ModuleAuditFieldKind_MODULE_AUDIT_FIELD_KIND_STRING_ARRAY
	ModuleAuditEventVisibility_MODULE_AUDIT_EVENT_VISIBILITY_UNSPECIFIED = v1.ModuleAuditEventVisibility_MODULE_AUDIT_EVENT_VISIBILITY_UNSPECIFIED
	ModuleAuditEventVisibility_MODULE_AUDIT_EVENT_VISIBILITY_TENANT      = v1.ModuleAuditEventVisibility_MODULE_AUDIT_EVENT_VISIBILITY_TENANT
	ModuleAuditEventVisibility_MODULE_AUDIT_EVENT_VISIBILITY_EXTERNAL    = v1.ModuleAuditEventVisibility_MODULE_AUDIT_EVENT_VISIBILITY_EXTERNAL
)
