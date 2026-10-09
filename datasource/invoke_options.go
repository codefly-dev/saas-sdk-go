package datasource

import "time"

// InvokeOption configures one call; the client retains no per-effect state.
type InvokeOption func(*invokeOptions)

type invokeOptions struct {
	effectID string
	deadline time.Time
}

// WithEffectID reuses a caller-owned effect ID. The host replays an identical
// request and refuses different input under the same ID. An empty ID mints one.
func WithEffectID(id string) InvokeOption {
	return func(o *invokeOptions) { o.effectID = id }
}

// WithDeadline bounds the call by an absolute deadline. An earlier deadline on
// ctx still wins. Expiry does not prove that a provider mutation did not happen.
func WithDeadline(deadline time.Time) InvokeOption {
	return func(o *invokeOptions) { o.deadline = deadline }
}
