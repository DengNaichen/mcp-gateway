package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
)

var updateDoc = flag.Bool("update", false, "rewrite docs/api/openapi.json from apiRoutes")

const openAPIDocPath = "../../docs/api/openapi.json"

// TestOpenAPIDocumentIsCurrent fails when an apitypes request/response field or
// a route changes without regenerating the committed document.
func TestOpenAPIDocumentIsCurrent(t *testing.T) {
	got, err := OpenAPIDocument()
	if err != nil {
		t.Fatal(err)
	}
	if *updateDoc {
		if err := os.WriteFile(openAPIDocPath, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(openAPIDocPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("docs/api/openapi.json is stale; regenerate it with\n  go test ./internal/api -run TestOpenAPIDocumentIsCurrent -update")
	}
}

// handlerSource is the parsed non-test source of package api.
type handlerSource struct {
	funcs    map[string]*ast.FuncDecl
	methods  map[string]*ast.FuncDecl // methods of *server
	handlers map[string]*ast.FuncLit  // "METHOD /api/path" -> handler
	patterns []string
}

func parseHandlers(t *testing.T) handlerSource {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	src := handlerSource{funcs: map[string]*ast.FuncDecl{}, methods: map[string]*ast.FuncDecl{}, handlers: map[string]*ast.FuncLit{}}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				src.funcs[fn.Name.Name] = fn
			} else if ok {
				src.methods[fn.Name.Name] = fn
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			pattern, _ := strconv.Unquote(lit.Value)
			method, path, hasMethod := strings.Cut(pattern, " ")
			if !hasMethod || !strings.HasPrefix(path, "/api/") {
				return true
			}
			key := method + " " + path
			src.patterns = append(src.patterns, key)
			if fn, ok := call.Args[1].(*ast.FuncLit); ok {
				src.handlers[key] = fn
			}
			return true
		})
	}
	slices.Sort(src.patterns)
	return src
}

func routeKeys() []string {
	keys := []string{}
	for _, r := range apiRoutes {
		keys = append(keys, r.Method+" "+r.Path)
	}
	slices.Sort(keys)
	return keys
}

// TestRouteTableMatchesMux fails when a route is registered without being
// documented in apiRoutes, or documented without being registered.
func TestRouteTableMatchesMux(t *testing.T) {
	src := parseHandlers(t)
	if documented := routeKeys(); !slices.Equal(src.patterns, documented) {
		t.Fatalf("apiRoutes and registered /api routes differ\n registered: %v\n documented: %v", src.patterns, documented)
	}
}

func typeName(v any) string {
	t := reflect.TypeOf(v)
	prefix := ""
	if t.Kind() == reflect.Slice {
		prefix, t = "[]", t.Elem()
	}
	return prefix + "apitypes." + t.Name()
}

// exprTypes resolves the apitypes type(s) an expression evaluates to, following
// local variables declared with an explicit type and package-level helpers
// (wireX conversions, mapSlice, and helpers returning any).
func (src handlerSource) exprTypes(e ast.Expr, body *ast.BlockStmt) ([]string, error) {
	switch e := e.(type) {
	case *ast.CompositeLit:
		return []string{types.ExprString(e.Type)}, nil
	case *ast.Ident:
		var found string
		ast.Inspect(body, func(n ast.Node) bool {
			if spec, ok := n.(*ast.ValueSpec); ok && spec.Type != nil && slices.ContainsFunc(spec.Names, func(id *ast.Ident) bool { return id.Name == e.Name }) {
				found = types.ExprString(spec.Type)
			}
			return found == ""
		})
		if found == "" {
			return nil, fmt.Errorf("variable %s has no declared type", e.Name)
		}
		return []string{found}, nil
	case *ast.CallExpr:
		fun, ok := e.Fun.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("cannot resolve %s", types.ExprString(e))
		}
		if fun.Name == "mapSlice" {
			elem, err := src.exprTypes(&ast.CallExpr{Fun: e.Args[1]}, body)
			if err != nil || len(elem) != 1 {
				return nil, fmt.Errorf("mapSlice element: %v", err)
			}
			return []string{"[]" + elem[0]}, nil
		}
		decl, ok := src.funcs[fun.Name]
		if !ok || decl.Type.Results == nil || len(decl.Type.Results.List) != 1 {
			return nil, fmt.Errorf("cannot resolve call to %s", fun.Name)
		}
		if result := types.ExprString(decl.Type.Results.List[0].Type); result != "any" {
			return []string{result}, nil
		}
		var out []string
		var resolveErr error
		ast.Inspect(decl.Body, func(n ast.Node) bool {
			if ret, ok := n.(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
				got, err := src.exprTypes(ret.Results[0], decl.Body)
				resolveErr = errorsJoin(resolveErr, err)
				out = append(out, got...)
			}
			return true
		})
		return out, resolveErr
	}
	return nil, fmt.Errorf("cannot resolve %s", types.ExprString(e))
}

func errorsJoin(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func statusCode(e ast.Expr) (int, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		n, err := strconv.Atoi(e.Value)
		return n, err == nil
	case *ast.SelectorExpr:
		switch e.Sel.Name {
		case "StatusOK":
			return 200, true
		case "StatusAccepted":
			return 202, true
		}
	}
	return 0, false
}

// collect records the decode targets and respond(status, value) types in body,
// following *server helper methods that receive the handler's ResponseWriter,
// since such helpers can write the response themselves.
func (src handlerSource) collect(t *testing.T, body *ast.BlockStmt, decoded *[]string, got map[int][]string, visited map[string]bool) {
	t.Helper()
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			helper := src.methods[sel.Sel.Name]
			passesWriter := slices.ContainsFunc(call.Args, func(a ast.Expr) bool { id, ok := a.(*ast.Ident); return ok && id.Name == "w" })
			if helper != nil && passesWriter && !visited[sel.Sel.Name] {
				visited[sel.Sel.Name] = true
				src.collect(t, helper.Body, decoded, got, visited)
			}
			return true
		}
		name, _ := call.Fun.(*ast.Ident)
		if name == nil {
			return true
		}
		switch name.Name {
		case "decode", "decodeRaw", "decodeRawLimit":
			target, ok := call.Args[2].(*ast.UnaryExpr)
			if !ok {
				t.Errorf("%s target must be &variable", name.Name)
				return true
			}
			typ, err := src.exprTypes(target.X, body)
			if err != nil {
				t.Error(err)
			}
			*decoded = append(*decoded, typ...)
		case "respond":
			status, ok := statusCode(call.Args[1])
			if !ok {
				t.Errorf("respond status %s must be a constant", types.ExprString(call.Args[1]))
				return true
			}
			typ, err := src.exprTypes(call.Args[2], body)
			if err != nil {
				t.Errorf("respond(%d): %v", status, err)
			}
			got[status] = append(got[status], typ...)
		}
		return true
	})
}

// TestHandlersUseDocumentedTypes fails when a handler decodes or responds with
// a type other than the one apiRoutes documents, or with an untyped map.
func TestHandlersUseDocumentedTypes(t *testing.T) {
	src := parseHandlers(t)
	for _, r := range apiRoutes {
		key := r.Method + " " + r.Path
		t.Run(key, func(t *testing.T) {
			fn := src.handlers[key]
			if fn == nil {
				t.Fatal("handler must be a function literal so its types can be checked")
			}
			var decoded []string
			got := map[int][]string{}
			src.collect(t, fn.Body, &decoded, got, map[string]bool{})

			var wantDecoded []string
			switch body := r.Request.(type) {
			case nil, rawBody:
			default:
				wantDecoded = []string{typeName(body)}
			}
			if !slices.Equal(decoded, wantDecoded) {
				t.Errorf("handler decodes %v; apiRoutes documents %v", decoded, wantDecoded)
			}

			want := map[int][]string{}
			for status, body := range r.Responses {
				switch body := body.(type) {
				case binary:
				case oneOf:
					for _, v := range body {
						want[status] = append(want[status], typeName(v))
					}
				default:
					want[status] = []string{typeName(body)}
				}
			}
			for status := range got {
				slices.Sort(got[status])
				got[status] = slices.Compact(got[status])
			}
			for status := range want {
				slices.Sort(want[status])
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("handler responds %v; apiRoutes documents %v", got, want)
			}
		})
	}
}

// TestResponsesMatchSchema checks real responses of a live backend against the
// committed document, so the generated schemas describe what the wire carries.
func TestResponsesMatchSchema(t *testing.T) {
	raw, err := os.ReadFile(openAPIDocPath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("urn:gateway:api", doc); err != nil {
		t.Fatal(err)
	}
	pointer := strings.NewReplacer("~", "~0", "/", "~1")

	g, svcs, err := gateway.New(t.TempDir(), "http://localhost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	handler := Handler(g, svcs)
	check := func(method, target, documentedPath string, body any) {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(b)
		} else {
			reader = bytes.NewReader(nil)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest(method, "http://localhost"+target, reader))
		status := strconv.Itoa(out.Code)
		if out.Code >= 400 {
			status = "default"
		}
		location := fmt.Sprintf("urn:gateway:api#/paths/%s/%s/responses/%s/content/application~1json/schema", pointer.Replace(documentedPath), strings.ToLower(method), status)
		schema, err := compiler.Compile(location)
		if err != nil {
			t.Fatalf("%s %s -> %d: no documented schema: %v", method, target, out.Code, err)
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(out.Body.Bytes()))
		if err != nil {
			t.Fatalf("%s %s: %v", method, target, err)
		}
		if err := schema.Validate(value); err != nil {
			t.Errorf("%s %s -> %d does not match the document: %v\n%s", method, target, out.Code, err, out.Body)
		}
	}

	server := apitypes.Server{ID: "ci", Name: "CI", Endpoint: "https://ci.example.com", Enabled: true}
	check("POST", "/api/servers/openapi", "/api/servers/openapi", apitypes.ImportOpenAPIRequest{Server: server, Spec: openAPIFixture})
	check("PUT", "/api/servers", "/api/servers", apitypes.Server{ID: "docs", Name: "Docs", Kind: "http", Endpoint: "https://docs.example.com", Enabled: true})
	check("PUT", "/api/scopes/mode", "/api/scopes/mode", apitypes.ScopeModeRequest{Enforce: false})
	check("PUT", "/api/scopes/repositories", "/api/scopes/repositories", apitypes.SaveRepositoryRequest{Name: "repo", RootPaths: []string{t.TempDir()}})
	for _, path := range []string{"/api/health", "/api/bootstrap", "/api/events", "/api/auth/me", "/api/setup/status", "/api/scopes", "/api/maintenance", "/api/catalog"} {
		check("GET", path, path, nil)
	}
	check("GET", "/api/tools/ci.logs/schema", "/api/tools/{id}/schema", nil)
	check("GET", "/api/maintenance/sources/ci/acceptance", "/api/maintenance/sources/{id}/acceptance", nil)
	check("GET", "/api/maintenance/runs/missing", "/api/maintenance/runs/{id}", nil)
	check("POST", "/api/servers/openapi", "/api/servers/openapi", apitypes.ImportOpenAPIRequest{Server: server})
	check("DELETE", "/api/model-profiles/missing", "/api/model-profiles/{id}", nil)
}
