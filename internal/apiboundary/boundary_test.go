// Package apiboundary holds the gate that keeps this module's public API free
// of its generated stub paths.
//
// The SDK repository is how a consumer gets this module's client: it carries a
// generated tree and releases it under its own tag. That tree is an
// implementation detail — it is regenerated whenever the contract moves, and it
// may one day be replaced by a dependency a registry serves instead of a
// directory committed here. Such a move stays *source*-compatible for a
// consumer only while no consumer ever names the tree, which holds only while
// every type reachable through a facade package can be named through that
// facade. (Source compatibility is not the whole story: two packages generated
// from the same .proto in one binary collide in protobuf's global file
// registry. Aliases keep consumer source unchanged; they do not by themselves
// make the swap safe.)
//
// A Go type alias makes the re-export free: `type Datasource = v1.Datasource`
// is the same type, so the indirection costs nothing and breaks nobody.
//
// This gate asks a *type* question, not a textual one, because that is the
// question the invariant is actually about. It loads every public package with
// full type information and walks the transitive surface a consumer can reach —
// exported funcs, vars, consts, struct fields, interface and named-type
// methods. A generated type anywhere in that surface must be re-exported by an
// alias in a public package, and a generated enum's constants must be
// re-exported too: a type alias carries the type but *not* the package-level
// constants declared with it, so aliasing `DatasourceStatus` alone still leaves
// a consumer unable to write a comparison without importing the stub tree.
//
// Walking types rather than syntax is what makes the gate hard to fool. An
// earlier syntactic version missed an unaliased `gen/` import (the package is
// named `accountsv1`, not the `v1` its path ends in), anything declared in
// types.go, and every exported var and const. testdata/fixture is a module
// that reproduces each of those bypasses; TestGateCatchesKnownBypasses asserts
// the gate reports them, because a gate that has quietly stopped looking is
// indistinguishable from a clean tree.
//
// Re-exports are pooled across public packages rather than required per
// package: if datasource re-exports Datasource, accounts may expose it too.
// That is deliberate and matches the invariant — the consumer names the type
// through a sibling facade and still never imports the stub tree.
package apiboundary

import (
	"bufio"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// finding is one violation of the boundary rule.
type finding struct {
	Pkg  string // the public package whose surface exposes the type
	Type string // "pkgname.TypeName" of the generated type
	Kind string // kindUnexported or kindMissingConst
	Msg  string
}

const (
	kindUnexported   = "not-re-exported"
	kindMissingConst = "missing-constant"
)

// analyze runs the boundary rule over the module rooted at dir and returns
// every violation. It is a plain function rather than inline test code so the
// gate itself can be tested against fixture modules that deliberately break it.
func analyze(dir string) ([]finding, error) {
	root, modulePath, err := moduleInfo(dir)
	if err != nil {
		return nil, err
	}
	generatedPrefix := modulePath + "/gen/"

	pkgs, err := loadPublicPackages(root, modulePath)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no public packages discovered under %s; the gate would pass vacuously", root)
	}

	// A generated type is legitimately part of the public API exactly when a
	// public package re-exports it under its own name. Collect those first:
	// the aliases are the mechanism, so they are never themselves violations.
	reExported := map[*types.TypeName]string{}
	reExportedConsts := map[*types.TypeName]map[string]bool{}
	for _, pkg := range pkgs {
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() {
				continue
			}
			switch obj := obj.(type) {
			case *types.TypeName:
				if !obj.IsAlias() {
					continue
				}
				if named, ok := types.Unalias(obj.Type()).(*types.Named); ok {
					if isUnder(named.Obj(), generatedPrefix) {
						reExported[named.Obj()] = pkg.PkgPath + "." + name
					}
				}
			case *types.Const:
				named, ok := types.Unalias(obj.Type()).(*types.Named)
				if !ok || !isUnder(named.Obj(), generatedPrefix) {
					continue
				}
				if reExportedConsts[named.Obj()] == nil {
					reExportedConsts[named.Obj()] = map[string]bool{}
				}
				reExportedConsts[named.Obj()][obj.Val().String()] = true
			}
		}
	}

	var findings []finding
	for _, pkg := range pkgs {
		rel := strings.TrimPrefix(strings.TrimPrefix(pkg.PkgPath, modulePath), "/")
		w := &walker{
			modulePath:      modulePath,
			generatedPrefix: generatedPrefix,
			seen:            map[types.Type]bool{},
			reached:         map[*types.TypeName]string{},
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() {
				continue
			}
			w.walk(obj.Type(), pkg.Name+"."+name)
		}

		for _, obj := range sortedTypeNames(w.reached) {
			qualified := obj.Pkg().Name() + "." + obj.Name()
			alias, ok := reExported[obj]
			if !ok {
				findings = append(findings, finding{
					Pkg: rel, Type: qualified, Kind: kindUnexported,
					Msg: fmt.Sprintf("%s is reachable from the public API via %s but is not re-exported; "+
						"add `%s = v1.%s` to this package's types.go so a consumer never imports %s",
						qualified, w.reached[obj], obj.Name(), obj.Name(), obj.Pkg().Path()),
				})
				continue
			}
			// A type alias carries the type, never the constants declared with
			// it. For a proto enum that leaves a consumer able to hold the
			// value and unable to compare it.
			for _, missing := range missingConstants(obj, reExportedConsts[obj]) {
				findings = append(findings, finding{
					Pkg: rel, Type: qualified, Kind: kindMissingConst,
					Msg: fmt.Sprintf("%s is re-exported as %s, but its constant %s is not; a consumer "+
						"can hold the value and cannot name it. Add a `const ... = v1.%s` re-export",
						obj.Name(), alias, missing, missing),
				})
			}
		}
	}
	return findings, nil
}

func TestPublicAPINamesEveryTypeItExposes(t *testing.T) {
	findings, err := analyze(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		t.Errorf("%s: %s", f.Pkg, f.Msg)
	}
}

// walker records every generated type reachable through a consumer-visible
// path, and the path that reached it.
type walker struct {
	modulePath      string
	generatedPrefix string
	seen            map[types.Type]bool
	reached         map[*types.TypeName]string
}

func (w *walker) walk(t types.Type, path string) {
	if t == nil {
		return
	}
	t = types.Unalias(t)
	if w.seen[t] {
		return
	}
	w.seen[t] = true

	switch t := t.(type) {
	case *types.Named:
		obj := t.Obj()
		if obj.Pkg() == nil { // a universe type such as error
			return
		}
		if isUnder(obj, w.generatedPrefix) {
			if _, ok := w.reached[obj]; !ok {
				w.reached[obj] = path
			}
		}
		// Stop at a type owned by another module. It cannot expose this
		// module's stub tree: reaching it would require importing this module,
		// which is an import cycle the compiler already forbids.
		pkgPath := obj.Pkg().Path()
		if pkgPath != w.modulePath && !strings.HasPrefix(pkgPath, w.modulePath+"/") {
			return
		}
		w.walk(t.Underlying(), path)
		for i := 0; i < t.NumMethods(); i++ {
			if m := t.Method(i); m.Exported() {
				w.walk(m.Type(), path+"."+m.Name()+"()")
			}
		}
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			// An unexported field — `inner accountsv1connect.AuditServiceClient`
			// — is how a facade is meant to be built: a consumer can neither
			// read it nor set it, so the path never reaches their source.
			if f := t.Field(i); f.Exported() {
				w.walk(f.Type(), path+"."+f.Name())
			}
		}
	case *types.Interface:
		for i := 0; i < t.NumMethods(); i++ {
			if m := t.Method(i); m.Exported() {
				w.walk(m.Type(), path+"."+m.Name()+"()")
			}
		}
	case *types.Signature:
		w.walkTuple(t.Params(), path+" param")
		w.walkTuple(t.Results(), path+" result")
	case *types.Pointer:
		w.walk(t.Elem(), path)
	case *types.Slice:
		w.walk(t.Elem(), path+"[]")
	case *types.Array:
		w.walk(t.Elem(), path+"[]")
	case *types.Chan:
		w.walk(t.Elem(), path)
	case *types.Map:
		w.walk(t.Key(), path+" key")
		w.walk(t.Elem(), path+" value")
	}
}

func (w *walker) walkTuple(tuple *types.Tuple, path string) {
	if tuple == nil {
		return
	}
	for i := 0; i < tuple.Len(); i++ {
		w.walk(tuple.At(i).Type(), path)
	}
}

// missingConstants returns the exported constants declared with obj's type in
// obj's own package that no public package re-exports. It is empty for any type
// that is not constant-valued, which is every proto message.
func missingConstants(obj *types.TypeName, have map[string]bool) []string {
	basic, ok := obj.Type().Underlying().(*types.Basic)
	if !ok || basic.Info()&(types.IsInteger|types.IsString) == 0 {
		return nil
	}
	var missing []string
	scope := obj.Pkg().Scope()
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if !ok || !c.Exported() {
			continue
		}
		named, ok := types.Unalias(c.Type()).(*types.Named)
		if !ok || named.Obj() != obj {
			continue
		}
		if !have[c.Val().String()] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// loadPublicPackages discovers — rather than enumerates — every package a
// consumer can import: everything in the module that is neither the generated
// tree nor internal. A list maintained by hand is the convention this gate
// exists to replace, and it drifts the first time somebody adds a package.
func loadPublicPackages(root, modulePath string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports,
		Dir:  root,
	}
	loaded, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("load packages under %s: %w", root, err)
	}
	var public []*packages.Package
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("load %s: %w", pkg.PkgPath, pkg.Errors[0])
		}
		if pkg.Types == nil || !strings.HasPrefix(pkg.PkgPath, modulePath) {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(pkg.PkgPath, modulePath), "/")
		if rel == "gen" || strings.HasPrefix(rel, "gen/") {
			continue
		}
		if rel == "internal" || strings.HasPrefix(rel, "internal/") || strings.Contains(rel, "/internal/") {
			continue
		}
		public = append(public, pkg)
	}
	sort.Slice(public, func(i, j int) bool { return public[i].PkgPath < public[j].PkgPath })
	return public, nil
}

func isUnder(obj *types.TypeName, prefix string) bool {
	return obj.Pkg() != nil && strings.HasPrefix(obj.Pkg().Path(), prefix)
}

func sortedTypeNames(m map[*types.TypeName]string) []*types.TypeName {
	out := make([]*types.TypeName, 0, len(m))
	for obj := range m {
		out = append(out, obj)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Pkg().Path(), out[j].Pkg().Path(); a != b {
			return a < b
		}
		return out[i].Name() < out[j].Name()
	})
	return out
}

// moduleInfo returns the module root at or above start and its declared path.
// The path is read from go.mod rather than hardcoded so that renaming the
// module cannot silently turn the generated-tree check into a prefix that
// matches nothing.
func moduleInfo(start string) (root, modulePath string, err error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	for {
		gomod := filepath.Join(dir, "go.mod")
		if _, statErr := os.Stat(gomod); statErr == nil {
			path, err := modulePathFrom(gomod)
			if err != nil {
				return "", "", err
			}
			return dir, path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("no go.mod at or above %s", start)
		}
		dir = parent
	}
}

func modulePathFrom(gomod string) (string, error) {
	f, err := os.Open(gomod)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no module directive in %s", gomod)
}
