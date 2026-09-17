// Package thing stands in for a registry-served module whose import path
// happens to contain "/gen/" — buf.build/gen/go/... is a real example already
// in this repo's go.mod. Exposing one of its types is legitimate: it is an
// independently versioned public dependency, not this module's private tree.
package thing

// ExtThing is exposed by a fixture facade and must never be reported.
type ExtThing struct{ Name string }
