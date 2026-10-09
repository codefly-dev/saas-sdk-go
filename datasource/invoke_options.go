package datasource

import "time"

// InvokeOption configures one call; the client retains no per-effect state.
type InvokeOption func(*invokeOptions)

type invokeOptions struct {
	effectID    string
	effectIDSet bool
	deadline    time.Time
	deadlineSet bool
}

// WithEffectID reuses a caller-owned effect ID. The host replays an identical
// request and refuses different input under the same ID. IDs must contain 1–128
// UTF-8 bytes without control bytes or surrounding spaces, so the header and
// request field remain identical. Omit this option to mint an ID.
func WithEffectID(id string) InvokeOption {
	return func(o *invokeOptions) { o.effectID, o.effectIDSet = id, true }
}

// WithDeadline bounds the call by an absolute deadline. An earlier deadline on
// ctx still wins. A deadline expired before dispatch returns InputError with no
// result. Expiry after dispatch does not prove that a mutation did not happen.
func WithDeadline(deadline time.Time) InvokeOption {
	return func(o *invokeOptions) { o.deadline, o.deadlineSet = deadline, true }
}
