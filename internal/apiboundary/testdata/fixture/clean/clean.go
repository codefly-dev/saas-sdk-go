// Package clean is the control: every generated type it exposes, and every
// type reachable through them, is re-exported here, constants included.
package clean

import v1 "fixturemod/gen/fake/v1"

type (
	CleanThing = v1.CleanThing
	CleanInner = v1.CleanInner
	CleanState = v1.CleanState
)

const (
	CleanStateUnspecified = v1.CleanState_CLEAN_STATE_UNSPECIFIED
	CleanStateOn          = v1.CleanState_CLEAN_STATE_ON
)

func Get() *CleanThing { return nil }
