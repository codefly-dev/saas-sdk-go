// Package externalgen exercises C5: the exposed type comes from a module whose
// path contains "/gen/" but is not this module's stub tree. It must not be
// reported — a substring rule false-positives here.
package externalgen

import "ext.example/gen/go/thing"

type ExtThing = thing.ExtThing

func Get() *ExtThing { return nil }
