package datasource

import v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"

// The message types this package's methods return, re-exported under the
// facade's own name — see the same block in package accounts for why an
// exported signature must never name the generated package.
type (
	// Datasource is the non-secret projection of a connected source.
	Datasource = v1.Datasource
	// DatasourceStatus is the ingestion status carried by a Datasource.
	DatasourceStatus = v1.DatasourceStatus
)
