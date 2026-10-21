// Package oneof aliases a message and one of its oneof wrappers but not the
// other. The field's own type is unexported and cannot be aliased; the wrappers
// are what a consumer names to set it.
package oneof

import v1 "fixturemod/gen/fake/v1"

type (
	OneofThing      = v1.OneofThing
	OneofThing_Left = v1.OneofThing_Left
)

func Get() *OneofThing { return nil }
