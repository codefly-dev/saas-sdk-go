// Package fakev1 stands in for the generated stub tree. Its package name is
// deliberately NOT the last segment of its import path ("v1"), mirroring the
// real accountsv1 package — a syntactic gate that guesses the local name from
// the path misses every unaliased import of it.
package fakev1

// Each scenario gets its own types so that a re-export in one fixture package
// cannot satisfy the rule for another.

type CleanThing struct {
	Name  string
	Inner *CleanInner
	State CleanState
}
type CleanInner struct{ Note string }
type CleanState int32

const (
	CleanState_CLEAN_STATE_UNSPECIFIED CleanState = 0
	CleanState_CLEAN_STATE_ON          CleanState = 1
)

type UnaliasedThing struct{ Name string }
type TypesFileThing struct{ Name string }
type VarThing struct{ Name string }

type EnumThing struct {
	State EnumState
}
type EnumState int32

const (
	EnumState_ENUM_STATE_UNSPECIFIED EnumState = 0
	EnumState_ENUM_STATE_ON          EnumState = 1
)

type TransitiveThing struct {
	Name  string
	Inner *TransitiveInner
}
type TransitiveInner struct{ Note string }
