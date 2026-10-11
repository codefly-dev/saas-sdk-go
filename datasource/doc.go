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
// Input and output use JSON-object strings in the host's bounded wire profile;
// numeric values never cross protobuf Struct's floating-point representation.
//
//	result, err := datasource.New(gw).Invoke(ctx, orgID, sourceID,
//		"list_invoices", map[string]any{"limit": 20})
//
// Invoke mints a UUIDv7 when WithEffectID is absent. Once a call is attempted,
// Result.Receipt.EffectID remains available even on error. Persist a caller-owned
// ID before a mutation if recovery must survive a process crash. The SDK never
// retries or deduplicates: the host alone replays byte-identical input_json under
// the same ID and refuses different input. Persist the encoded input with the
// ID and reuse json.RawMessage for replay. WithDeadline bounds the call, not
// the effect. An already-expired deadline returns InputError with no result
// before dispatch; a deadline expiring after dispatch leaves the outcome unknown.
//
// ErrOutcomeUnknown is a typed outcome: the provider may have acted. Lookup
// returns the receipt alongside that sentinel, tested with errors.Is. Retain the
// ID and look it up with a live context; do not infer a failed effect or mint a
// new ID to retry. See Example_invoke for the complete compiled example.
// Lookup's ErrEffectNotFound retains the ID for a safe explicit same-ID invoke;
// committed receipts include Output without another invocation.
//
// ErrOperationDeclarationRemoved is a distinct recovery outcome carrying the
// retained effect's ReceiptStatus (COMMITTED or UNKNOWN). Recover with Lookup,
// never with a fresh ID: a committed status here does not include the saved
// output. Lookup survives declaration removal/replacement under current source
// read authority; unresolved or expired receipts remain unknown.
//
// DatasourceError retains a valid COMMITTED receipt when either output JSON
// field is malformed. Receipt.Output uses receipt.output_json; Result.Output
// uses top-level output_json. Bad output never authorizes repeating a commit.
// Unsupported HTTP response encodings return DatasourceError on Declare/List
// before inferring a status; Invoke/Lookup retain OutcomeUnknown and the ID.
// ListOperations rejects an unspecified effect with InputError and an unknown
// future effect enum with DatasourceError, without returning a partial list.
package datasource
