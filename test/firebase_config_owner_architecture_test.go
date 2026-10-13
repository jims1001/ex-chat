package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// This private local read deliberately has no remote query alias. SYS retains
// storage; NTF receives only the two persisted values needed by its transport.
type architectureLocalFirebaseConfigOwnerPort struct {
	Consumer, Provider, Kind, Scope                             string
	Port, Method, DefinitionFile                                string
	ConsumerType, ConsumerFactory, ConsumerField, ConsumerFile  string
	ProviderRepository, ProviderRepositoryFile, ProviderFactory string
	ProviderTable                                               string
	Keys                                                        []string
	MaxRows, MaxValueBytes                                      int
	CompositionFile, CompositionFunction, CompositionMethod     string
	StandaloneFile                                              string
}

// Called from the existing architecture gate root, including in the native
// gate. A renamed field or split receiver cannot hide this credential read.
func validateArchitectureFirebaseConfigOwnerPort(t *testing.T, sources architectureDependencySources, modules map[string]architectureDependencyModule) {
	t.Helper()
	want := []architectureLocalFirebaseConfigOwnerPort{{
		Consumer: "NTF", Provider: "SYS", Kind: "synchronous", Scope: "local-private-firebase-config-only",
		Port: "service.FirebasePushConfigPort", Method: "FirebasePushConfiguration", DefinitionFile: "internal/service/push_service.go",
		ConsumerType: "PushService", ConsumerFactory: "NewPushService", ConsumerField: "firebaseConfig", ConsumerFile: "internal/service/push_fcm.go",
		ProviderRepository: "SystemConfigRepository", ProviderRepositoryFile: "internal/repository/system_config_firebase.go", ProviderFactory: "NewSystemConfigRepository", ProviderTable: "system_configs",
		Keys: []string{"FIREBASE_PROJECT_ID", "FIREBASE_CREDENTIALS"}, MaxRows: 3, MaxValueBytes: 32768,
		CompositionFile: "internal/router/router.go", CompositionFunction: "SetupRouterWithOptions", CompositionMethod: "SetFirebaseConfigOwner", StandaloneFile: "cmd/module-server/main.go",
	}}
	if !reflect.DeepEqual(sources.LocalFirebaseConfigOwnerPorts, want) {
		t.Fatalf("private Firebase owner-port inventory changed: got %+v, want %+v", sources.LocalFirebaseConfigOwnerPorts, want)
	}
	port := want[0]
	allowed := false
	for _, provider := range modules[port.Consumer].Synchronous {
		allowed = allowed || provider == port.Provider
	}
	if !allowed || sources.RepositoryOwners[port.ProviderRepository] != port.Provider || sources.RepositoryOwners[port.ConsumerType] != port.Consumer {
		t.Fatal("private Firebase read requires the real synchronous NTF -> SYS owner edge")
	}
	if len(modules[port.Consumer].Startup) != 0 {
		t.Fatal("private Firebase configuration must not add an NTF startup dependency")
	}
	ntfFiles := map[string]bool{}
	for _, scope := range sources.ScanScopes {
		if scope.Consumer == port.Consumer {
			ntfFiles[scope.File] = true
		}
	}
	for _, path := range []string{port.DefinitionFile, port.ConsumerFile} {
		count := 0
		for _, scope := range sources.ScanScopes {
			if scope.File == path {
				if scope.Consumer != port.Consumer {
					t.Fatalf("%s must retain its NTF source owner", path)
				}
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s requires exactly one NTF scan scope", path)
		}
	}

	files := map[string]*ast.File{}
	for _, root := range []string{"internal", "cmd"} {
		if err := filepath.WalkDir("../"+root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			files[strings.TrimPrefix(filepath.ToSlash(path), "../")] = file
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) *ast.File {
		t.Helper()
		if files[path] == nil {
			t.Fatalf("missing private Firebase source %s", path)
		}
		return files[path]
	}
	typ := func(path, name string) ast.Expr {
		t.Helper()
		var found ast.Expr
		ast.Inspect(read(path), func(n ast.Node) bool {
			if spec, ok := n.(*ast.TypeSpec); ok && spec.Name.Name == name {
				if spec.Assign.IsValid() || spec.TypeParams != nil || found != nil {
					t.Fatalf("%s must retain one concrete non-generic definition", name)
				}
				found = spec.Type
			}
			return true
		})
		if found == nil {
			t.Fatalf("missing %s in %s", name, path)
		}
		return found
	}
	fn := func(path, receiver, name string) *ast.FuncDecl {
		t.Helper()
		var found *ast.FuncDecl
		for _, declaration := range read(path).Decls {
			f, ok := declaration.(*ast.FuncDecl)
			if !ok || f.Name.Name != name {
				continue
			}
			if receiver == "" && f.Recv == nil || f.Recv != nil && len(f.Recv.List) == 1 && reportCountASTText(t, f.Recv.List[0].Type) == receiver {
				if found != nil {
					t.Fatalf("duplicate private Firebase function %s.%s", receiver, name)
				}
				found = f
			}
		}
		if found == nil {
			t.Fatalf("missing %s.%s in %s", receiver, name, path)
		}
		return found
	}
	definition, ok := typ(port.DefinitionFile, "FirebasePushConfigPort").(*ast.InterfaceType)
	if !ok || len(definition.Methods.List) != 1 || len(definition.Methods.List[0].Names) != 1 || definition.Methods.List[0].Names[0].Name != port.Method {
		t.Fatal("Firebase port must expose only its fixed-pair read, without embedded capabilities")
	}
	signature, ok := definition.Methods.List[0].Type.(*ast.FuncType)
	wantSignature := []string{"|", "context.Context", "|", "string", "string", "error"}
	provider := fn(port.ProviderRepositoryFile, "*"+port.ProviderRepository, port.Method)
	if !ok || !reflect.DeepEqual(reportCountSignature(t, signature), wantSignature) || !reflect.DeepEqual(reportCountSignature(t, provider.Type), wantSignature) {
		t.Fatal("Firebase owner signature must accept only context and return the fixed pair and error")
	}
	consumer, ok := typ(port.DefinitionFile, port.ConsumerType).(*ast.StructType)
	if !ok {
		t.Fatal("PushService must retain its explicit owner field")
	}
	fieldCount := 0
	for _, field := range consumer.Fields.List {
		for _, name := range field.Names {
			if name.Name == port.ConsumerField {
				if reportCountASTText(t, field.Type) != "FirebasePushConfigPort" {
					t.Fatal("PushService Firebase configuration must use the narrow port")
				}
				fieldCount++
			}
		}
	}
	if fieldCount != 1 {
		t.Fatal("PushService must retain exactly one private Firebase owner field")
	}
	setter := fn(port.DefinitionFile, "*"+port.ConsumerType, port.CompositionMethod)
	if !reflect.DeepEqual(reportCountSignature(t, setter.Type), []string{"|", "FirebasePushConfigPort", "|"}) || reportCountASTText(t, setter.Body) != "{\n\ts.firebaseConfig = owner\n}" {
		t.Fatal("Firebase setter must only install the explicitly composed SYS owner")
	}

	// Query guards pin the fixed-key projection and bounded read, not a generic
	// SystemConfig Get/List capability. The third row detects malformed duplicates.
	queryArgs := map[string][]string{
		"WithContext": {"ctx"},
		"Model":       {"&domain.SystemConfig{}"},
		"Select":      {`"config_key, CASE WHEN " + length + " <= ? THEN value ELSE NULL END AS value"`, "32768"},
		"Where":       {`"config_key IN ?"`, `[]string{"FIREBASE_PROJECT_ID", "FIREBASE_CREDENTIALS"}`},
		"Limit":       {"3"},
		"Rows":        {},
	}
	seenQuery := map[string]int{}
	allowedProviderCalls := map[string]bool{"New": true, "Name": true, "Close": true, "Next": true, "Scan": true, "ValidString": true, "Err": true}
	ast.Inspect(provider.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			if reportCountASTText(t, call.Fun) != "len" {
				t.Fatalf("private Firebase read gained an unregistered helper: %s", reportCountASTText(t, call))
			}
			return true
		}
		if args, guarded := queryArgs[method.Sel.Name]; guarded {
			actual := []string{}
			for _, arg := range call.Args {
				actual = append(actual, reportCountASTText(t, arg))
			}
			if !reflect.DeepEqual(actual, args) {
				t.Fatalf("private Firebase %s query arguments changed: %v", method.Sel.Name, actual)
			}
			seenQuery[method.Sel.Name]++
		} else if !allowedProviderCalls[method.Sel.Name] {
			t.Fatalf("private Firebase read gained an unregistered operation: %s", reportCountASTText(t, call))
		}
		return true
	})
	for method := range queryArgs {
		if seenQuery[method] != 1 {
			t.Fatalf("private Firebase query requires exactly one %s call", method)
		}
	}
	for _, imported := range read(port.ProviderRepositoryFile).Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if imported.Name != nil || path != "context" && path != "errors" && path != "unicode/utf8" && path != "github.com/OracleBetX-Projects/ex-chat/internal/domain" {
			t.Fatalf("private Firebase SYS read gained an unregistered import %s", path)
		}
	}

	// Inventory production expressions, including their containing functions.
	// Extracting a method value or moving a receiver does not bypass these checks.
	type callSite struct{ path, function, expression string }
	expected := map[callSite]int{
		{port.ConsumerFile, "selectNativeFCM", "s.firebaseConfig.FirebasePushConfiguration(ctx)"}:                                        1,
		{port.CompositionFile, port.CompositionFunction, "service.NewPushService(db, deviceRepo)"}:                                       1,
		{port.CompositionFile, port.CompositionFunction, "pushService.SetFirebaseConfigOwner(repository.NewSystemConfigRepository(db))"}: 1,
	}
	tracked := map[string]bool{port.Method: true, port.CompositionMethod: true, port.ConsumerFactory: true}
	for path, file := range files {
		for _, declaration := range file.Decls {
			function := ""
			if f, ok := declaration.(*ast.FuncDecl); ok {
				function = f.Name.Name
				if f.Recv != nil && len(f.Recv.List) == 1 {
					receiver := strings.TrimPrefix(reportCountASTText(t, f.Recv.List[0].Type), "*")
					if receiver == port.ConsumerType && path != port.DefinitionFile && path != port.ConsumerFile || receiver == "nativeFCM" && path != port.ConsumerFile {
						t.Fatalf("unregistered NTF Firebase split receiver %s in %s", receiver, path)
					}
				}
			}
			called := map[ast.Expr]bool{}
			ast.Inspect(declaration, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					called[call.Fun] = true
					name := ""
					switch method := call.Fun.(type) {
					case *ast.SelectorExpr:
						name = method.Sel.Name
					case *ast.Ident:
						name = method.Name
					}
					if tracked[name] {
						site := callSite{path, function, reportCountASTText(t, call)}
						if expected[site] == 0 {
							t.Fatalf("unregistered private Firebase capability/composition call: %+v", site)
						}
						expected[site]--
					}
				}
				return true
			})
			ast.Inspect(declaration, func(n ast.Node) bool {
				if selector, ok := n.(*ast.SelectorExpr); ok && tracked[selector.Sel.Name] && !called[selector] {
					t.Fatalf("private Firebase method must be called only at its registered site: %s in %s", reportCountASTText(t, selector), path)
				}
				if literal, ok := n.(*ast.CompositeLit); ok && literal.Type != nil && strings.TrimPrefix(reportCountASTText(t, literal.Type), "service.") == port.ConsumerType {
					if path != port.DefinitionFile || function != port.ConsumerFactory {
						t.Fatalf("PushService must be constructed through its registered factory: %s", path)
					}
					for _, element := range literal.Elts {
						if field, ok := element.(*ast.KeyValueExpr); ok && reportCountASTText(t, field.Key) == port.ConsumerField {
							t.Fatal("PushService constructor must not infer a SYS owner from its NTF store")
						}
					}
				}
				if id, ok := n.(*ast.Ident); ok {
					if path == port.StandaloneFile && (tracked[id.Name] || id.Name == port.ConsumerType || id.Name == "FirebasePushConfigPort") {
						t.Fatal("standalone module-server must not imply a local native Firebase composition")
					}
					if ntfFiles[path] && (id.Name == "SystemConfig" || id.Name == port.ProviderRepository || id.Name == port.ProviderFactory) {
						t.Fatalf("NTF must not directly retain SYS configuration models or repositories in %s", path)
					}
				}
				if literal, ok := n.(*ast.BasicLit); ok && literal.Kind == token.STRING && ntfFiles[path] {
					value, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(strings.ToLower(value), port.ProviderTable) {
						t.Fatalf("NTF must not directly query SYS configuration storage in %s", path)
					}
				}
				return true
			})
		}
	}
	for site, remaining := range expected {
		if remaining != 0 {
			t.Fatalf("missing registered private Firebase call: %+v", site)
		}
	}
}
