// Package apiboundary holds the gate that keeps this module's public API free
// of its generated stub paths.
//
// The SDK repository is how a consumer gets this module's client: it carries a
// generated tree and releases it under its own tag. That tree is an
// implementation detail — it is regenerated whenever the contract moves, and it
// may one day be replaced by a dependency a registry serves instead of a
// directory committed here. Neither is a breaking change for a consumer *as
// long as no consumer ever names it*, which holds only while every exported
// signature of a facade package refers to the facade's own re-exported types.
//
// A Go type alias makes that free: `type Datasource = v1.Datasource` is the
// same type, so the indirection costs nothing and breaks nobody. The rule is
// easy to violate by accident — adding one method that returns a `*v1.Foo`
// re-opens the leak — so it is a test rather than a convention.
package apiboundary

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// facadePackages are the directories whose exported API a consumer calls.
// A new facade package belongs here the day it is created.
var facadePackages = []string{"accounts", "datasource", "settings", "workcontext"}

// generatedImport marks an import path as part of the generated tree.
const generatedImport = "/gen/"

func TestNoExportedSignatureNamesTheGeneratedTree(t *testing.T) {
	root := moduleRoot(t)
	for _, pkg := range facadePackages {
		t.Run(pkg, func(t *testing.T) {
			dir := filepath.Join(root, pkg)
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("facade package %s does not exist; update facadePackages", pkg)
			}
			fset := token.NewFileSet()
			parsed, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
				// types.go is where the re-exports live: aliasing a generated
				// type is the mechanism, not a violation of it.
				return !strings.HasSuffix(info.Name(), "_test.go") && info.Name() != "types.go"
			}, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", dir, err)
			}
			for _, astPkg := range parsed {
				for name, file := range astPkg.Files {
					for _, alias := range generatedImportAliases(file) {
						for _, decl := range exportedSignatures(file) {
							if pos := usesAlias(decl.node, alias); pos.IsValid() {
								t.Errorf("%s: exported %s names the generated package through %q — "+
									"re-export the type in types.go and use that name, so a consumer "+
									"never imports %s",
									fset.Position(pos), decl.name, alias, generatedImport)
								_ = name
							}
						}
					}
				}
			}
		})
	}
}

// generatedImportAliases returns the local names a file binds to the generated
// tree, including a dot or underscore import, which would leak just as well.
func generatedImportAliases(file *ast.File) []string {
	var aliases []string
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.Contains(path, generatedImport) {
			continue
		}
		if spec.Name != nil {
			aliases = append(aliases, spec.Name.Name)
			continue
		}
		aliases = append(aliases, path[strings.LastIndex(path, "/")+1:])
	}
	return aliases
}

type signature struct {
	name string
	node ast.Node
}

// exportedSignatures returns the parts of a file a consumer can name: an
// exported func's parameters and results (never its body, which may use the
// generated package freely), and the reachable surface of an exported type.
//
// For a struct that means its *exported* fields only. An unexported field
// holding a generated client — `inner accountsv1connect.AuditServiceClient` —
// is precisely how a facade is supposed to be built: a consumer can neither
// read it nor set it, so the path never reaches their source. Same for an
// interface's unexported methods.
func exportedSignatures(file *ast.File) []signature {
	var out []signature
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() || !receiverIsExported(d) {
				continue
			}
			out = append(out, signature{name: "func " + d.Name.Name, node: d.Type})
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				out = append(out, exportedTypeSurface("type "+ts.Name.Name, ts.Type)...)
			}
		}
	}
	return out
}

// exportedTypeSurface reduces a type to what a consumer can reach through it.
func exportedTypeSurface(name string, expr ast.Expr) []signature {
	switch t := expr.(type) {
	case *ast.StructType:
		return exportedFields(name+" field", t.Fields)
	case *ast.InterfaceType:
		return exportedFields(name+" method", t.Methods)
	default:
		// A defined or aliased type exposes whatever it is defined as.
		return []signature{{name: name, node: expr}}
	}
}

func exportedFields(label string, fields *ast.FieldList) []signature {
	var out []signature
	if fields == nil {
		return out
	}
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			// Embedded: its name IS its type, so a consumer can reach it.
			out = append(out, signature{name: label + " (embedded)", node: field.Type})
			continue
		}
		for _, fieldName := range field.Names {
			if !fieldName.IsExported() {
				continue
			}
			out = append(out, signature{name: label + " " + fieldName.Name, node: field.Type})
		}
	}
	return out
}

// receiverIsExported keeps an exported method on an unexported type out of the
// check: a consumer cannot name its receiver, so it cannot call it.
func receiverIsExported(d *ast.FuncDecl) bool {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return true
	}
	name := d.Recv.List[0].Type
	if star, ok := name.(*ast.StarExpr); ok {
		name = star.X
	}
	ident, ok := name.(*ast.Ident)
	return ok && ident.IsExported()
}

// usesAlias reports the position at which node selects from alias, if it does.
func usesAlias(node ast.Node, alias string) token.Pos {
	var found token.Pos
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == alias && !found.IsValid() {
			found = sel.Pos()
			return false
		}
		return true
	})
	return found
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}
