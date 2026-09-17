// Package unaliased exercises C2: the gen import carries no explicit alias, so
// its local name is the package's own name (fakev1), not the "v1" its path ends
// in. A gate that derives the name from the path sees nothing here.
package unaliased

import "fixturemod/gen/fake/v1"

func Get() *fakev1.UnaliasedThing { return nil }
