// Package enumconst aliases the enum type but not the constants declared with
// it — the C1 shape that leaves a consumer able to read the value and unable to
// compare it.
package enumconst

import v1 "fixturemod/gen/fake/v1"

type (
	EnumThing = v1.EnumThing
	EnumState = v1.EnumState
)

func Get() *EnumThing { return nil }
