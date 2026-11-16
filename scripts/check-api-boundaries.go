//go:build ignore

// This checker parses imports and selector expressions, including aliased imports
// and platform files, so HTTP clients remain legal while handler types do not.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	handlers := map[string]bool{"Handler": true, "HandlerFunc": true, "ResponseWriter": true, "ServeMux": true, "NewServeMux": true, "Server": true, "ListenAndServe": true, "ListenAndServeTLS": true, "Serve": true, "ServeTLS": true, "NewCrossOriginProtection": true}
	err := filepath.WalkDir("internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(filepath.Dir(path))
		httpName := ""
		for _, im := range f.Imports {
			imported, _ := strconv.Unquote(im.Path.Value)
			if imported == "net/http" {
				if pkg == "internal/gateway" {
					return fmt.Errorf("gateway imports net/http: %s", path)
				}
				httpName = "http"
				if im.Name != nil {
					httpName = im.Name.Name
				}
				if httpName == "." {
					return fmt.Errorf("dot HTTP import defeats ownership check: %s", path)
				}
			}
			if imported == "net/http/httptest" && pkg != "internal/contracttest" {
				return fmt.Errorf("synthetic peers belong in contracttest: %s", path)
			}
		}
		if httpName == "" || pkg == "internal/api" || pkg == "internal/contracttest" {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			se, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := se.X.(*ast.Ident)
			if !ok || x.Name != httpName || !handlers[se.Sel.Name] {
				return true
			}
			// OAuth's private loopback callback is part of the outbound client flow.
			if filepath.ToSlash(path) == "internal/upstream/oauth.go" && (se.Sel.Name == "NewServeMux" || se.Sel.Name == "ResponseWriter" || se.Sel.Name == "Server") {
				return true
			}
			err = fmt.Errorf("HTTP handler outside api: %s: %s.%s", path, httpName, se.Sel.Name)
			return false
		})
		return err
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("PASS: API handler ownership; gateway has no net/http; OAuth callback and synthetic contract peers are isolated")
}
