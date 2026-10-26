package buffer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestWaitFreeSource checks the package source for the constructs that could
// block or retry: a sync import, read-modify-write atomics, and any loop.
func TestWaitFreeSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			switch path {
			case "errors", "fmt", "sync/atomic":
			default:
				t.Errorf("%s imports %q", name, path)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ForStmt, *ast.RangeStmt, *ast.GoStmt, *ast.SelectStmt, *ast.SendStmt:
				t.Errorf("%s: %T at %v", name, n, fset.Position(n.Pos()))
			case *ast.SelectorExpr:
				switch n.Sel.Name {
				case "CompareAndSwap", "Swap", "Add", "And", "Or":
					t.Errorf("%s: read-modify-write atomic %s at %v", name, n.Sel.Name, fset.Position(n.Pos()))
				}
			case *ast.UnaryExpr:
				if n.Op == token.ARROW {
					t.Errorf("%s: channel receive at %v", name, fset.Position(n.Pos()))
				}
			}
			return true
		})
	}
	if checked < 3 {
		t.Fatalf("checked %d source files, want at least 3", checked)
	}
}
