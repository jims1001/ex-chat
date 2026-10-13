package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The legacy summary has optional bounds and no daily-series budget. Register
// its narrower integer-only MSG capability independently of daily bucket ports.
func validateArchitectureReportSummaryOwnerPort(t *testing.T, sources architectureDependencySources, modules map[string]architectureDependencyModule) {
	t.Helper()
	want := []architectureLocalCountOwnerPort{{Consumer: "RPT", Provider: "MSG", Kind: "synchronous", Scope: "local-summary-count-only", Port: "service.ReportMessageSummaryPort", Method: "CountReportSummary", DefinitionFile: "internal/service/report_message_summary.go", ConsumerFieldFile: "internal/service/report_service.go", ConsumerField: "summaryMessageCounts", ConsumerFile: "internal/service/report_service.go", ProviderRepository: "MessageRepository", ProviderRepositoryFile: "internal/repository/message_report_summary.go", ProviderTable: "messages", ProviderFactory: "NewMessageRepository", DefaultCompositionFile: "internal/service/report_service.go", DefaultCompositionFunction: "NewReportService", CompositionFile: "internal/router/router.go", CompositionMethod: "SetSummaryMessageOwner"}}
	if !reflect.DeepEqual(sources.LocalSummaryOwnerPorts, want) {
		t.Fatalf("summary owner-port inventory changed: got %+v, want %+v", sources.LocalSummaryOwnerPorts, want)
	}
	allowed := false
	for _, provider := range modules["RPT"].Synchronous {
		allowed = allowed || provider == "MSG"
	}
	if !allowed || sources.RepositoryOwners["MessageRepository"] != "MSG" {
		t.Fatal("summary capability must retain RPT to MSG ownership")
	}
	files := map[string]*ast.File{}
	for _, root := range []string{"internal", "cmd"} {
		err := filepath.WalkDir("../"+root, func(path string, entry fs.DirEntry, err error) error {
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
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	typ := func(path, name string) ast.Expr {
		var result ast.Expr
		ast.Inspect(files[path], func(n ast.Node) bool {
			if spec, ok := n.(*ast.TypeSpec); ok && spec.Name.Name == name {
				result = spec.Type
			}
			return true
		})
		if result == nil {
			t.Fatalf("missing %s in %s", name, path)
		}
		return result
	}
	fn := func(path, receiver, name string) *ast.FuncDecl {
		for _, d := range files[path].Decls {
			f, ok := d.(*ast.FuncDecl)
			if !ok || f.Name.Name != name {
				continue
			}
			if receiver == "" && f.Recv == nil || f.Recv != nil && reportCountASTText(t, f.Recv.List[0].Type) == receiver {
				return f
			}
		}
		t.Fatalf("missing %s.%s", receiver, name)
		return nil
	}
	port := want[0]
	definition, ok := typ(port.DefinitionFile, "ReportMessageSummaryPort").(*ast.InterfaceType)
	if !ok || len(definition.Methods.List) != 1 || len(definition.Methods.List[0].Names) != 1 || definition.Methods.List[0].Names[0].Name != port.Method {
		t.Fatal("summary capability must expose only its owned count method")
	}
	signature := []string{"|", "uint", "*time.Time", "*time.Time", "|", "domain.ReportMessageSummary", "error"}
	if !reflect.DeepEqual(reportCountSignature(t, definition.Methods.List[0].Type.(*ast.FuncType)), signature) || !reflect.DeepEqual(reportCountSignature(t, fn(port.ProviderRepositoryFile, "*MessageRepository", port.Method).Type), signature) {
		t.Fatal("summary owner signature must contain only account, optional bounds, integer DTO and error")
	}
	dto := typ("internal/domain/report_message_summary.go", "ReportMessageSummary").(*ast.StructType)
	if len(dto.Fields.List) != 3 {
		t.Fatal("summary DTO must contain exactly three counts")
	}
	for i, field := range dto.Fields.List {
		if len(field.Names) != 1 || field.Names[0].Name != []string{"Total", "Incoming", "Outgoing"}[i] || reportCountASTText(t, field.Type) != "int64" {
			t.Fatal("summary DTO must contain only int64 counts")
		}
	}
	fieldFound := false
	for _, f := range typ(port.ConsumerFieldFile, "ReportService").(*ast.StructType).Fields.List {
		for _, n := range f.Names {
			if n.Name == port.ConsumerField {
				fieldFound = reportCountASTText(t, f.Type) == "ReportMessageSummaryPort"
			}
		}
	}
	if !fieldFound {
		t.Fatal("summary consumer field must be typed by its count port")
	}
	factoryFound := false
	ast.Inspect(fn(port.DefaultCompositionFile, "", port.DefaultCompositionFunction).Body, func(n ast.Node) bool {
		if kv, ok := n.(*ast.KeyValueExpr); ok && reportCountASTText(t, kv.Key) == port.ConsumerField {
			factoryFound = reportCountASTText(t, kv.Value) == "repository.NewMessageRepository(db)"
		}
		return true
	})
	if !factoryFound {
		t.Fatal("default summary composition must install MSG owner")
	}
	setter := fn(port.DefinitionFile, "*ReportService", port.CompositionMethod)
	if !reflect.DeepEqual(reportCountSignature(t, setter.Type), []string{"|", "ReportMessageSummaryPort", "|"}) || reportCountASTText(t, setter.Body) != "{\n\ts.summaryMessageCounts = messages\n}" {
		t.Fatal("summary setter must install explicit MSG capability")
	}
	calls, wiring := 0, 0
	for path, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if method.Sel.Name == port.Method {
				if path != port.ConsumerFile || reportCountASTText(t, call) != "s.summaryMessageCounts.CountReportSummary(accountID, filter.Since, filter.Until)" {
					t.Fatalf("unregistered summary capability call in %s", path)
				}
				calls++
			}
			if method.Sel.Name == port.CompositionMethod {
				if path != port.CompositionFile || reportCountASTText(t, call) != "reportService.SetSummaryMessageOwner(msgRepo)" {
					t.Fatalf("unregistered summary composition in %s", path)
				}
				wiring++
			}
			return true
		})
	}
	if calls != 1 || wiring != 1 {
		t.Fatalf("summary owner must have one call and one router composition: %d, %d", calls, wiring)
	}
}
