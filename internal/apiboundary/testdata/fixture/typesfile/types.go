// Package typesfile exercises C3: the violation is declared in types.go, the
// file a gate that skips the whole file by name cannot see.
package typesfile

import v1 "fixturemod/gen/fake/v1"

func Get() *v1.TypesFileThing { return nil }
