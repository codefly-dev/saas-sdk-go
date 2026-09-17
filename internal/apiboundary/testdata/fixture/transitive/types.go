// Package transitive aliases the returned message but not the type of the
// field it exposes — the exact shape the real datasource facade shipped with.
package transitive

import v1 "fixturemod/gen/fake/v1"

type TransitiveThing = v1.TransitiveThing

func Get() *TransitiveThing { return nil }
