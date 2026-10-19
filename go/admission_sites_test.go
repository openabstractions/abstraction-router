package router

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The package and its importers own every production entry into Router's
// post-admission seam. Check each calling function, rather than a package-wide
// occurrence of a gate that could belong to another entry point.
func TestRouterPostAdmissionCallSites(t *testing.T) {
	rootBytes, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSpace(string(rootBytes))
	command := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "*.go")
	command.Dir = root
	files, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Split(strings.TrimSpace(string(files)), "\n") {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "Answer") || ast.IsGenerated(mustParse(t, name, body, parser.ParseComments)) {
			continue
		}
		file := mustParse(t, name, body, 0)
		eligible := strings.HasPrefix(name, "openabstractions-flat/abstraction-router/go/")
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			eligible = eligible || path == "github.com/openabstractions/abstraction-router/go"
		}
		if !eligible {
			continue
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var seamCalls []*ast.CallExpr
			var admissions []token.Pos
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if fn.Name.Name == "Remote" && fun.Name == "policy" {
						admissions = append(admissions, call.Pos())
					}
				case *ast.SelectorExpr:
					if fun.Sel.Name == "Check" || fun.Sel.Name == "authorize" {
						admissions = append(admissions, call.Pos())
					}
					if fun.Sel.Name == "Answer" || fun.Sel.Name == "AnswerContext" || fun.Sel.Name == "AnswerRemoteContext" {
						seamCalls = append(seamCalls, call)
					}
				}
				return true
			})
			// Answer is the public context-free forwarding form of the same
			// post-admission seam; it introduces no transport entry point.
			forward := name == "openabstractions-flat/abstraction-router/go/service.go" && fn.Name.Name == "Answer" && len(fn.Body.List) == 1
			for _, call := range seamCalls {
				admitted := forward
				for _, admission := range admissions {
					admitted = admitted || admission < call.Pos()
				}
				if !admitted {
					t.Errorf("%s: %s calls a post-admission Router method without its admission owner", name, fn.Name.Name)
				}
			}
		}
	}
}

func mustParse(t *testing.T, name string, body []byte, mode parser.Mode) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, body, mode)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
