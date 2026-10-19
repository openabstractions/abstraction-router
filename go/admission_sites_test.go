package router

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The package and its importers own every production entry into Router's
// post-admission seam. Check each calling function, rather than a package-wide
// occurrence of a gate that could belong to another entry point.
func TestRouterPostAdmissionCallSites(t *testing.T) {
	files := routerSourceFiles(t)
	if !slices.ContainsFunc(files, func(f routerSourceFile) bool { return f.name == routerModulePrefix+"service.go" }) {
		t.Fatal("the source inventory misses the module's own service.go")
	}
	for _, source := range files {
		name := source.name
		body, err := os.ReadFile(source.path)
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

// routerModulePrefix is this module's folder in the repository.
const routerModulePrefix = "openabstractions-flat/abstraction-router/go/"

// routerSourceFile is one Go file: its slash-separated name in the
// repository and its path on this computer.
type routerSourceFile struct{ name, path string }

// routerSourceFiles lists every non-test Go file of the checkout this
// package's tests run in, read from the file system. The test runs in the module folder;
// in the repository the root is three folders up, and in a checkout of the
// module alone the module folder is the root and its files carry the
// module's repository prefix. Folders git ignores or that hold other
// checkouts and build output are skipped: those whose name starts with a
// dot (.git, .forks, .split, .build, .gotest), node_modules, bin, build and
// target. A worktree reached from another operating system, as a Windows
// worktree under WSL, has a .git file git there cannot follow, so the
// inventory never asks git.
func routerSourceFiles(t *testing.T) []routerSourceFile {
	t.Helper()
	module, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, prefix := filepath.Clean(filepath.Join(module, "..", "..", "..")), ""
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(routerModulePrefix), "service.go")); err != nil {
		root, prefix = module, routerModulePrefix
	}
	var files []routerSourceFile
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || slices.Contains([]string{"node_modules", "bin", "build", "target"}, name)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, routerSourceFile{name: prefix + filepath.ToSlash(rel), path: path})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func mustParse(t *testing.T, name string, body []byte, mode parser.Mode) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, body, mode)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
