package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"golang.org/x/xerrors"
)

func TestDoctorInspectCallsAfterLargeStoreReturnAreMapped(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("doctor.go")
	if err != nil {
		t.Fatalf("ReadFile(doctor.go) error = %v", err)
	}
	calls, err := inspectCallsAfterLargeStoreReturn(src)
	if err != nil {
		t.Fatalf("inspectCallsAfterLargeStoreReturn() error = %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("found no inspect calls after the large-store return")
	}
	foundConversion := false
	for _, call := range calls {
		if call == "inspectConsolidationConversion" {
			foundConversion = true
		}
		if _, ok := doctorInspectCallCheckNames[call]; !ok {
			t.Fatalf("inspect %s is appended after the large-store return but is not mapped for bounded coverage", call)
		}
	}
	if !foundConversion {
		t.Fatal("inspectConsolidationConversion is not among default-path calls after the large-store return")
	}
}

func inspectCallsAfterLargeStoreReturn(src []byte) ([]string, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "doctor.go", src, 0)
	if err != nil {
		return nil, xerrors.Errorf("parse doctor.go: %w", err)
	}
	var calls []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "buildDoctorReport" || fn.Body == nil {
			continue
		}
		collect := false
		for _, stmt := range fn.Body.List {
			ifStmt, ok := stmt.(*ast.IfStmt)
			if ok && strings.Contains(exprIdentChain(ifStmt.Cond), "isLargeStoreForBoundedDoctor") {
				collect = true
				continue
			}
			if !collect {
				continue
			}
			if _, ok := stmt.(*ast.RangeStmt); ok {
				break
			}
			ast.Inspect(stmt, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := selectorOrIdent(call.Fun)
				if strings.HasPrefix(name, "inspect") {
					calls = append(calls, name)
				}
				return true
			})
		}
	}
	return uniqueInspectCalls(calls), nil
}

func selectorOrIdent(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func exprIdentChain(expr ast.Expr) string {
	var parts []string
	ast.Inspect(expr, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if ok {
			parts = append(parts, ident.Name)
		}
		return true
	})
	return strings.Join(parts, ".")
}

func uniqueInspectCalls(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
