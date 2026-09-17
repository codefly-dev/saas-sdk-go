package apiboundary

import (
	"sort"
	"strings"
	"testing"
)

// TestGateCatchesKnownBypasses runs the gate against testdata/fixture, a module
// built to break it in every way a previous syntactic version could be broken.
// The go tool ignores testdata, so the fixture is invisible to the real gate.
//
// Without this, the gate's own correctness rests on nothing: it reports no
// findings against a clean tree whether it works or is silently looking in the
// wrong place.
func TestGateCatchesKnownBypasses(t *testing.T) {
	findings, err := analyze("testdata/fixture")
	if err != nil {
		t.Fatalf("analyze fixture: %v", err)
	}

	got := map[string][]string{}
	for _, f := range findings {
		got[f.Pkg] = append(got[f.Pkg], f.Kind+" "+f.Type)
	}
	for _, v := range got {
		sort.Strings(v)
	}

	want := map[string][]string{
		// C2: unaliased gen import — local name is fakev1, path ends in "v1".
		"unaliased": {kindUnexported + " fakev1.UnaliasedThing"},
		// C3: violation declared in types.go.
		"typesfile": {kindUnexported + " fakev1.TypesFileThing"},
		// C4: exported var rather than a func or type declaration.
		"exportedvar": {kindUnexported + " fakev1.VarThing"},
		// C1: the returned message is re-exported, the field's type is not.
		"transitive": {kindUnexported + " fakev1.TransitiveInner"},
		// C1: the enum type is re-exported, its constants are not.
		"enumconst": {
			kindMissingConst + " fakev1.EnumState",
			kindMissingConst + " fakev1.EnumState",
		},
		// Control: fully re-exported, nothing to report.
		"clean": nil,
		// C5: "/gen/" in a *dependency's* path is not this module's stub tree.
		"externalgen": nil,
	}

	for pkg, wantKinds := range want {
		gotKinds := got[pkg]
		if len(gotKinds) != len(wantKinds) {
			t.Errorf("%s: got %d findings %v, want %d %v",
				pkg, len(gotKinds), gotKinds, len(wantKinds), wantKinds)
			continue
		}
		for i := range wantKinds {
			if gotKinds[i] != wantKinds[i] {
				t.Errorf("%s: finding %d = %q, want %q", pkg, i, gotKinds[i], wantKinds[i])
			}
		}
	}
	for pkg := range got {
		if _, expected := want[pkg]; !expected {
			t.Errorf("unexpected findings for fixture package %s: %v", pkg, got[pkg])
		}
	}
}

// TestGateRejectsAVacuousPass guards the failure mode that would make every
// other assertion here meaningless: a discovery bug that finds no public
// packages reports zero findings, which is indistinguishable from a clean tree.
func TestGateRejectsAVacuousPass(t *testing.T) {
	_, err := analyze("testdata/internalonly")
	if err == nil {
		t.Fatal("analyze of a module with no public packages returned no error")
	}
	if !strings.Contains(err.Error(), "vacuously") {
		t.Fatalf("expected a vacuous-pass error, got %v", err)
	}
}
