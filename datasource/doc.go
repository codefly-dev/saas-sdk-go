// Package datasource connects sources and calls their declared operations
// through the host's DatasourceService. New binds the generated Connect client
// to an existing Gateway; the gateway supplies the person's Work Context.
// The host owns credentials, provider routes, receipts, replay and audit.
//
// AddGitHubSource, ListSources and Sync are the connection and ingestion half.
// A creation credential is sent once and sealed by the host; source reads
// never return it. Invoke, Lookup, DeclareOperations and ListOperations are
// the call half. A caller supplies JSON input and an operation name, never a
// provider URL or credential.
//
//	result, err := datasource.New(gw).Invoke(ctx, orgID, sourceID,
//		"list_invoices", map[string]any{"limit": 20})
//
// Invoke mints a UUIDv7 when WithEffectID is absent. Once a call is attempted,
// Result.Receipt.EffectID remains available even on error. Persist a caller-owned
// ID before a mutation if recovery must survive a process crash. The SDK never
// retries or deduplicates: the host alone replays identical input under the same
// ID and refuses different input. WithDeadline bounds the call, not the effect.
//
// ErrOutcomeUnknown is a typed outcome: the provider may have acted. Lookup
// returns the receipt alongside that sentinel, tested with errors.Is. Retain the
// ID and look it up with a live context; do not infer a failed effect or mint a
// new ID to retry. See Example_invoke for the complete compiled example.
package datasource
