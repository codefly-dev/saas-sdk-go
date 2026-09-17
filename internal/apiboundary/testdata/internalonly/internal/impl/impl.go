// Package impl is the only package in its module, and it is internal — so the
// module has no public API at all.
package impl

// Impl is unreachable by any consumer.
type Impl struct{ Name string }
