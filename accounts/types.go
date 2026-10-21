package accounts

import v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"

// The message types this package's methods take and return, re-exported under
// the facade's own name.
//
// A consumer must be able to call every method here while importing only this
// package. Naming `gen/...` in an exported signature would put that path into
// consumer source, which fixes where the stubs come from: the generated tree is
// this module's private implementation detail, and it moves — a regeneration
// against a newer contract, or one day a dependency on a registry-served
// package instead of a committed tree. A Go type alias makes these the *same*
// types, so this costs nothing at runtime and breaks no existing caller that
// already names the generated package.
//
// Re-exporting the types named in a signature is not sufficient on its own:
// every type reachable *through* them has to be nameable here too, and an
// enum needs its constants re-exported because an alias does not carry them.
// internal/apiboundary enforces both.
type (
	// QueryAuditLogRequest is the request for AuditClient.QueryAuditLog.
	QueryAuditLogRequest = v1.QueryAuditLogRequest
	// QueryAuditLogResponse is the response from AuditClient.QueryAuditLog.
	QueryAuditLogResponse = v1.QueryAuditLogResponse
	// AuditEvent is one entry of a QueryAuditLogResponse.
	AuditEvent = v1.AuditEvent
)

// The DatasourcesClient messages, under their generated names: this facade is
// the raw contract, and package datasource is the ergonomic one. Package
// datasource re-exports some of the same types under idiomatic constant names;
// both name the same Go types.
type (
	ListSourcesRequest                    = v1.ListSourcesRequest
	ListSourcesResponse                   = v1.ListSourcesResponse
	GetSourceRequest                      = v1.GetSourceRequest
	GetSourceResponse                     = v1.GetSourceResponse
	AddSourceRequest                      = v1.AddSourceRequest
	AddSourceResponse                     = v1.AddSourceResponse
	ListSourceOperationsRequest           = v1.ListSourceOperationsRequest
	ListSourceOperationsResponse          = v1.ListSourceOperationsResponse
	DeclareSourceOperationsRequest        = v1.DeclareSourceOperationsRequest
	DeclareSourceOperationsResponse       = v1.DeclareSourceOperationsResponse
	InvokeSourceOperationRequest          = v1.InvokeSourceOperationRequest
	InvokeSourceOperationResponse         = v1.InvokeSourceOperationResponse
	LookupInvokeSourceOperationRequest    = v1.LookupInvokeSourceOperationRequest
	BeginDatasourceAccountLinkRequest     = v1.BeginDatasourceAccountLinkRequest
	BeginDatasourceAccountLinkResponse    = v1.BeginDatasourceAccountLinkResponse
	CompleteDatasourceAccountLinkRequest  = v1.CompleteDatasourceAccountLinkRequest
	CompleteDatasourceAccountLinkResponse = v1.CompleteDatasourceAccountLinkResponse
	ListMyDatasourceAccountLinksRequest   = v1.ListMyDatasourceAccountLinksRequest
	ListMyDatasourceAccountLinksResponse  = v1.ListMyDatasourceAccountLinksResponse
)

// Every message and oneof wrapper reachable through a DatasourcesClient message,
// so a consumer can build `&AddSourceRequest{Config: &AddSourceRequest_Api{...}}`.
type (
	AddSourceRequest_Api             = v1.AddSourceRequest_Api
	AddSourceRequest_BoundaryNodeId  = v1.AddSourceRequest_BoundaryNodeId
	AddSourceRequest_CollectionLabel = v1.AddSourceRequest_CollectionLabel
	AddSourceRequest_Crawler         = v1.AddSourceRequest_Crawler
	AddSourceRequest_Github          = v1.AddSourceRequest_Github
	AddSourceRequest_Upload          = v1.AddSourceRequest_Upload
	ApiDatasourceConfig              = v1.ApiDatasourceConfig
	ApiOAuth2Config                  = v1.ApiOAuth2Config
	CrawlerDatasourceConfig          = v1.CrawlerDatasourceConfig
	Datasource                       = v1.Datasource
	DatasourceAccountLink            = v1.DatasourceAccountLink
	GitHubDatasourceConfig           = v1.GitHubDatasourceConfig
	SourceOperation                  = v1.SourceOperation
	SourceOperationReceipt           = v1.SourceOperationReceipt
	UploadDatasourceConfig           = v1.UploadDatasourceConfig
)

// The enums reachable through them. An alias carries the type and not its
// constants, so each value is re-exported too.
type (
	ApiCredentialKind      = v1.ApiCredentialKind
	ApiOAuth2Config_Grant  = v1.ApiOAuth2Config_Grant
	DatasourceLiveDelivery = v1.DatasourceLiveDelivery
	DatasourceProvider     = v1.DatasourceProvider
	DatasourceStatus       = v1.DatasourceStatus
	SourceOperation_Effect = v1.SourceOperation_Effect
)

const (
	ApiCredentialKind_API_CREDENTIAL_KIND_BASIC       = v1.ApiCredentialKind_API_CREDENTIAL_KIND_BASIC
	ApiCredentialKind_API_CREDENTIAL_KIND_BEARER      = v1.ApiCredentialKind_API_CREDENTIAL_KIND_BEARER
	ApiCredentialKind_API_CREDENTIAL_KIND_HEADER      = v1.ApiCredentialKind_API_CREDENTIAL_KIND_HEADER
	ApiCredentialKind_API_CREDENTIAL_KIND_OAUTH2      = v1.ApiCredentialKind_API_CREDENTIAL_KIND_OAUTH2
	ApiCredentialKind_API_CREDENTIAL_KIND_QUERY       = v1.ApiCredentialKind_API_CREDENTIAL_KIND_QUERY
	ApiCredentialKind_API_CREDENTIAL_KIND_UNSPECIFIED = v1.ApiCredentialKind_API_CREDENTIAL_KIND_UNSPECIFIED
)

const (
	ApiOAuth2Config_GRANT_AUTHORIZATION_CODE = v1.ApiOAuth2Config_GRANT_AUTHORIZATION_CODE
	ApiOAuth2Config_GRANT_CLIENT_CREDENTIALS = v1.ApiOAuth2Config_GRANT_CLIENT_CREDENTIALS
	ApiOAuth2Config_GRANT_REFRESH_TOKEN      = v1.ApiOAuth2Config_GRANT_REFRESH_TOKEN
	ApiOAuth2Config_GRANT_UNSPECIFIED        = v1.ApiOAuth2Config_GRANT_UNSPECIFIED
)

const (
	DatasourceLiveDelivery_DATASOURCE_LIVE_DELIVERY_APP_WEBHOOK    = v1.DatasourceLiveDelivery_DATASOURCE_LIVE_DELIVERY_APP_WEBHOOK
	DatasourceLiveDelivery_DATASOURCE_LIVE_DELIVERY_NONE           = v1.DatasourceLiveDelivery_DATASOURCE_LIVE_DELIVERY_NONE
	DatasourceLiveDelivery_DATASOURCE_LIVE_DELIVERY_SOURCE_WEBHOOK = v1.DatasourceLiveDelivery_DATASOURCE_LIVE_DELIVERY_SOURCE_WEBHOOK
	DatasourceLiveDelivery_DATASOURCE_LIVE_DELIVERY_UNSPECIFIED    = v1.DatasourceLiveDelivery_DATASOURCE_LIVE_DELIVERY_UNSPECIFIED
)

const (
	DatasourceProvider_DATASOURCE_PROVIDER_API         = v1.DatasourceProvider_DATASOURCE_PROVIDER_API
	DatasourceProvider_DATASOURCE_PROVIDER_CRAWLER     = v1.DatasourceProvider_DATASOURCE_PROVIDER_CRAWLER
	DatasourceProvider_DATASOURCE_PROVIDER_GITHUB      = v1.DatasourceProvider_DATASOURCE_PROVIDER_GITHUB
	DatasourceProvider_DATASOURCE_PROVIDER_UNSPECIFIED = v1.DatasourceProvider_DATASOURCE_PROVIDER_UNSPECIFIED
	DatasourceProvider_DATASOURCE_PROVIDER_UPLOAD      = v1.DatasourceProvider_DATASOURCE_PROVIDER_UPLOAD
)

const (
	DatasourceStatus_DATASOURCE_STATUS_ACTIVE      = v1.DatasourceStatus_DATASOURCE_STATUS_ACTIVE
	DatasourceStatus_DATASOURCE_STATUS_DEGRADED    = v1.DatasourceStatus_DATASOURCE_STATUS_DEGRADED
	DatasourceStatus_DATASOURCE_STATUS_PAUSED      = v1.DatasourceStatus_DATASOURCE_STATUS_PAUSED
	DatasourceStatus_DATASOURCE_STATUS_UNSPECIFIED = v1.DatasourceStatus_DATASOURCE_STATUS_UNSPECIFIED
)

const (
	SourceOperation_EFFECT_MUTATION    = v1.SourceOperation_EFFECT_MUTATION
	SourceOperation_EFFECT_READ_ONLY   = v1.SourceOperation_EFFECT_READ_ONLY
	SourceOperation_EFFECT_UNSPECIFIED = v1.SourceOperation_EFFECT_UNSPECIFIED
)
