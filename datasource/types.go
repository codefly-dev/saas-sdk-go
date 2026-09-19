package datasource

import v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"

// The message types this package's methods return, re-exported under the
// facade's own name — see the same block in package accounts for why an
// exported signature must never name the generated package.
//
// Everything a Datasource exposes belongs here too, not just Datasource
// itself: a consumer who can hold the value but cannot name the type of one of
// its fields is still forced to import the generated tree.
type (
	// Datasource is the non-secret projection of a connected source.
	Datasource = v1.Datasource
	// DatasourceStatus is the ingestion status carried by a Datasource.
	DatasourceStatus = v1.DatasourceStatus
	// DatasourceProvider identifies which backend a Datasource pulls from.
	DatasourceProvider = v1.DatasourceProvider
	// GitHubDatasourceConfig is the stored GitHub configuration of a
	// Datasource, as returned on Datasource.Github.
	GitHubDatasourceConfig = v1.GitHubDatasourceConfig
	// ApiDatasourceConfig is the non-secret configuration of an API source.
	ApiDatasourceConfig = v1.ApiDatasourceConfig
	// ApiOAuth2Config is the non-secret OAuth configuration of an API source.
	ApiOAuth2Config = v1.ApiOAuth2Config
	// ApiCredentialKind identifies how an API source authenticates.
	ApiCredentialKind = v1.ApiCredentialKind
	// CrawlerDatasourceConfig is the configuration of a public web crawler.
	CrawlerDatasourceConfig = v1.CrawlerDatasourceConfig
	// UploadDatasourceConfig is the non-secret configuration of an upload source.
	UploadDatasourceConfig = v1.UploadDatasourceConfig
)

// A type alias re-exports the type but not the constants declared with it, so
// the enum values need their own re-export: without them a consumer can read
// src.Status and still has no way to compare it.
const (
	// DatasourceStatusUnspecified is the zero value; the server has not set a
	// status.
	DatasourceStatusUnspecified = v1.DatasourceStatus_DATASOURCE_STATUS_UNSPECIFIED
	// DatasourceStatusActive means the source is being ingested.
	DatasourceStatusActive = v1.DatasourceStatus_DATASOURCE_STATUS_ACTIVE
	// DatasourceStatusPaused means ingestion is suspended for the source.
	DatasourceStatusPaused = v1.DatasourceStatus_DATASOURCE_STATUS_PAUSED
	// DatasourceStatusDegraded means ingestion requires operator attention.
	DatasourceStatusDegraded = v1.DatasourceStatus_DATASOURCE_STATUS_DEGRADED
)

const (
	// DatasourceProviderUnspecified is the zero value; no provider recorded.
	DatasourceProviderUnspecified = v1.DatasourceProvider_DATASOURCE_PROVIDER_UNSPECIFIED
	// DatasourceProviderGitHub is a GitHub repository source.
	DatasourceProviderGitHub = v1.DatasourceProvider_DATASOURCE_PROVIDER_GITHUB
	// DatasourceProviderAPI is an HTTP API source.
	DatasourceProviderAPI = v1.DatasourceProvider_DATASOURCE_PROVIDER_API
	// DatasourceProviderCrawler is a public web crawler source.
	DatasourceProviderCrawler = v1.DatasourceProvider_DATASOURCE_PROVIDER_CRAWLER
	// DatasourceProviderUpload is an object upload source.
	DatasourceProviderUpload = v1.DatasourceProvider_DATASOURCE_PROVIDER_UPLOAD
)

const (
	ApiCredentialKindUnspecified = v1.ApiCredentialKind_API_CREDENTIAL_KIND_UNSPECIFIED
	ApiCredentialKindBearer      = v1.ApiCredentialKind_API_CREDENTIAL_KIND_BEARER
	ApiCredentialKindBasic       = v1.ApiCredentialKind_API_CREDENTIAL_KIND_BASIC
	ApiCredentialKindHeader      = v1.ApiCredentialKind_API_CREDENTIAL_KIND_HEADER
	ApiCredentialKindQuery       = v1.ApiCredentialKind_API_CREDENTIAL_KIND_QUERY
	ApiCredentialKindOAuth2      = v1.ApiCredentialKind_API_CREDENTIAL_KIND_OAUTH2
)
