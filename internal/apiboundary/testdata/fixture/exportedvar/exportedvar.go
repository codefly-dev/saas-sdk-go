// Package exportedvar exercises C4: an exported var, not a func or type, puts
// the generated type in the consumer-reachable surface.
package exportedvar

import v1 "fixturemod/gen/fake/v1"

var Default = &v1.VarThing{}
