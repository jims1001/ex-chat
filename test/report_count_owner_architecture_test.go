package test

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Count ports are local synchronous capabilities. Keeping them separate from
// remote contracts and the SMTP adapter preserves both existing inventories.
// Their exact signatures also exclude object loading and storage handles.
type architectureLocalCountOwnerPort struct {
	Consumer, Provider, Kind, Scope                    string
	Port, Method, DefinitionFile                       string
	ConsumerFieldFile, ConsumerField, ConsumerFile     string
	ProviderRepository, ProviderRepositoryFile         string
	ProviderTable, ProviderFactory                     string
	DefaultCompositionFile, DefaultCompositionFunction string
	CompositionFile, CompositionMethod                 string
	MaxBuckets                                         int
}

func reportCountASTText(t *testing.T, node ast.Node) string {
	t.Helper()
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), node); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func reportCountSignature(t *testing.T, signature *ast.FuncType) []string {
	t.Helper()
	var result []string
	for _, fields := range []*ast.FieldList{signature.Params, signature.Results} {
		result = append(result, "|")
		if fields == nil {
			continue
		}
		for _, field := range fields.List {
			count := len(field.Names)
			if count == 0 {
				count = 1
			}
			for i := 0; i < count; i++ {
				result = append(result, reportCountASTText(t, field.Type))
			}
		}
	}
	return result
}

// Called by the existing architecture gate root, so interface spelling or
// lowercase field names cannot remove these dependencies from gate coverage.
func validateArchitectureReportCountOwnerPorts(t *testing.T, sources architectureDependencySources, modules map[string]architectureDependencyModule) {
	t.Helper()
	var want []architectureLocalCountOwnerPort
	for _, owner := range []struct{ provider, port, field, repository, file, table string }{
		{"CON", "ReportConversationCountPort", "conversationCounts", "ConversationRepository", "conversation_report_count.go", "conversations"},
		{"MSG", "ReportMessageCountPort", "messageCounts", "MessageRepository", "message_report_count.go", "messages"},
	} {
		want = append(want, architectureLocalCountOwnerPort{
			Consumer: "RPT", Provider: owner.provider, Kind: "synchronous", Scope: "local-count-only",
			Port: "service." + owner.port, Method: "CountReportBuckets", DefinitionFile: "internal/service/report_count_series.go",
			ConsumerFieldFile: "internal/service/report_service.go", ConsumerField: owner.field, ConsumerFile: "internal/service/report_count_series.go",
			ProviderRepository: owner.repository, ProviderRepositoryFile: "internal/repository/" + owner.file,
			ProviderTable: owner.table, ProviderFactory: "New" + owner.repository,
			DefaultCompositionFile: "internal/service/report_service.go", DefaultCompositionFunction: "NewReportService",
			CompositionFile: "internal/router/router.go", CompositionMethod: "SetCountOwners", MaxBuckets: 400,
		})
	}
	if !reflect.DeepEqual(sources.LocalCountOwnerPorts, want) {
		t.Fatalf("local count owner-port inventory changed: got %+v, want %+v", sources.LocalCountOwnerPorts, want)
	}
	// ReportHandler and ReportService already belong to RPT. Its split HTTP
	// method retains that owner and must not disappear from source coverage just
	// because the service field is declared in the companion handler file.
	handlerScope := false
	for _, scope := range sources.ScanScopes {
		if scope.File == "internal/handler/report_count_series.go" {
			handlerScope = scope.Consumer == "RPT"
		}
	}
	if !handlerScope {
		t.Fatal("split count-series handler must retain its explicit RPT source scope")
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
		file := files[path]
		if file == nil {
			t.Fatalf("missing count capability source %s", path)
		}
		return file
	}
	findType := func(path, name string) ast.Expr {
		t.Helper()
		var result ast.Expr
		ast.Inspect(read(path), func(node ast.Node) bool {
			if spec, ok := node.(*ast.TypeSpec); ok && spec.Name.Name == name {
				result = spec.Type
			}
			return true
		})
		if result == nil {
			t.Fatalf("missing count capability type %s in %s", name, path)
		}
		return result
	}
	findFunction := func(path, receiver, name string) *ast.FuncDecl {
		t.Helper()
		for _, declaration := range read(path).Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Name.Name != name {
				continue
			}
			if receiver == "" && fn.Recv == nil || fn.Recv != nil && len(fn.Recv.List) == 1 && reportCountASTText(t, fn.Recv.List[0].Type) == receiver {
				return fn
			}
		}
		t.Fatalf("missing count capability function %s.%s in %s", receiver, name, path)
		return nil
	}

	// Grouping remains an explicit count-only domain value. An unrestricted
	// string, ReportFilter expansion, or an additional grouping is not this port.
	if got := reportCountASTText(t, findType("internal/domain/report_count.go", "ReportCountGrouping")); got != "string" {
		t.Fatalf("count grouping underlying type changed: %s", got)
	}
	groupings := map[string]string{}
	for _, declaration := range read("internal/domain/report_count.go").Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range group.Specs {
			if groupingType, ok := spec.(*ast.TypeSpec); ok && groupingType.Name.Name == "ReportCountGrouping" && groupingType.Assign.IsValid() {
				t.Fatal("count grouping must be a defined enum type, not a string alias")
			}
			value, ok := spec.(*ast.ValueSpec)
			if !ok || value.Type == nil || reportCountASTText(t, value.Type) != "ReportCountGrouping" {
				continue
			}
			if group.Tok != token.CONST || len(value.Names) != 1 || len(value.Values) != 1 {
				t.Fatal("count groupings require explicit named typed constants")
			}
			groupings[value.Names[0].Name] = reportCountASTText(t, value.Values[0])
		}
	}
	if want := map[string]string{"ReportCountHour": `"hour"`, "ReportCountDay": `"day"`, "ReportCountMonth": `"month"`, "ReportCountYear": `"year"`}; !reflect.DeepEqual(groupings, want) {
		t.Fatalf("count grouping inventory changed: %v", groupings)
	}
	series := findFunction("internal/service/report_count_series.go", "*ReportService", "GetCountSeries")
	if got := reportCountSignature(t, series.Type); !reflect.DeepEqual(got, []string{"|", "context.Context", "uint", "string", "domain.ReportCountGrouping", "ReportFilter", "|", "[]domain.ReportCountPoint", "error"}) {
		t.Fatalf("count-series service signature changed: %v", got)
	}
	offsetSeries := findFunction("internal/service/report_count_series.go", "*ReportService", "GetCountSeriesWithOffset")
	if got := reportCountSignature(t, offsetSeries.Type); !reflect.DeepEqual(got, []string{"|", "context.Context", "uint", "string", "domain.ReportCountGrouping", "ReportFilter", "int64", "|", "[]domain.ReportCountPoint", "error"}) {
		t.Fatalf("fixed-offset count service signature changed: %v", got)
	}
	if reportCountASTText(t, series.Body) != "{\n\treturn s.GetCountSeriesWithOffset(ctx, accountID, metric, grouping, filter, 0)\n}" {
		t.Fatal("legacy count service must preserve the zero offset")
	}
	handler := findFunction("internal/handler/report_count_series.go", "*ReportHandler", "GetCountSeries")
	if got := reportCountSignature(t, handler.Type); !reflect.DeepEqual(got, []string{"|", "*gin.Context", "|"}) {
		t.Fatalf("count-series handler signature changed: %v", got)
	}
	helper := findFunction("internal/repository/report_count.go", "", "countReportBuckets")
	if got := reportCountSignature(t, helper.Type); !reflect.DeepEqual(got, []string{"|", "context.Context", "*gorm.DB", "uint", "string", "string", "domain.ReportCountGrouping", "[]domain.ReportCountBucket", "|", "[]domain.ReportCountPoint", "error"}) {
		t.Fatalf("count helper signature changed: %v", got)
	}

	callSites, helperSites := map[string]int{}, map[string]int{}
	for _, port := range sources.LocalCountOwnerPorts {
		allowed := false
		for _, provider := range modules[port.Consumer].Synchronous {
			allowed = allowed || provider == port.Provider
		}
		if !allowed || sources.RepositoryOwners[port.ProviderRepository] != port.Provider {
			t.Fatalf("count capability %s has no registered owner/dependency", port.Port)
		}
		name := strings.TrimPrefix(port.Port, "service.")
		definition, ok := findType(port.DefinitionFile, name).(*ast.InterfaceType)
		if !ok || len(definition.Methods.List) != 1 || len(definition.Methods.List[0].Names) != 1 || definition.Methods.List[0].Names[0].Name != port.Method {
			t.Fatalf("%s must expose only %s", port.Port, port.Method)
		}
		signature, ok := definition.Methods.List[0].Type.(*ast.FuncType)
		if !ok {
			t.Fatalf("%s count method is not a function", port.Port)
		}
		params := []string{"|", "context.Context", "uint"}
		if port.Provider == "MSG" {
			params = append(params, "string")
		}
		params = append(params, "domain.ReportCountGrouping", "[]domain.ReportCountBucket", "|", "[]domain.ReportCountPoint", "error")
		if got := reportCountSignature(t, signature); !reflect.DeepEqual(got, params) {
			t.Fatalf("%s lost its context/account/count-only signature: %v", port.Port, got)
		}
		owner := findFunction(port.ProviderRepositoryFile, "*"+port.ProviderRepository, port.Method)
		if !reflect.DeepEqual(reportCountSignature(t, owner.Type), params) {
			t.Fatalf("%s owner method differs from its count capability", port.Port)
		}
		serviceType, ok := findType(port.ConsumerFieldFile, "ReportService").(*ast.StructType)
		if !ok {
			t.Fatal("ReportService must remain a struct")
		}
		fieldFound := false
		for _, field := range serviceType.Fields.List {
			for _, fieldName := range field.Names {
				if fieldName.Name == port.ConsumerField {
					fieldFound = reportCountASTText(t, field.Type) == name
				}
			}
		}
		if !fieldFound {
			t.Fatalf("RPT field %s is not typed by %s", port.ConsumerField, port.Port)
		}
		constructor := findFunction(port.DefaultCompositionFile, "", port.DefaultCompositionFunction)
		factoryFound := false
		ast.Inspect(constructor.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || reportCountASTText(t, literal.Type) != "ReportService" {
				return true
			}
			for _, item := range literal.Elts {
				entry, ok := item.(*ast.KeyValueExpr)
				if ok && reportCountASTText(t, entry.Key) == port.ConsumerField {
					factoryFound = reportCountASTText(t, entry.Value) == "repository."+port.ProviderFactory+"(db)"
				}
			}
			return true
		})
		if !factoryFound {
			t.Fatalf("default RPT composition lacks owner factory for %s", port.ConsumerField)
		}
		callSites[port.ConsumerFile+"|"+port.ConsumerField] = 0
		helperSites[port.ProviderRepositoryFile] = 0
	}

	setter := findFunction(want[0].DefinitionFile, "*ReportService", "SetCountOwners")
	if got := reportCountSignature(t, setter.Type); !reflect.DeepEqual(got, []string{"|", "ReportConversationCountPort", "ReportMessageCountPort", "|"}) {
		t.Fatalf("count-owner setter signature changed: %v", got)
	}
	assignments := map[string]string{}
	ast.Inspect(setter.Body, func(node ast.Node) bool {
		if assignment, ok := node.(*ast.AssignStmt); ok && len(assignment.Lhs) == len(assignment.Rhs) {
			for i, lhs := range assignment.Lhs {
				assignments[reportCountASTText(t, lhs)] = reportCountASTText(t, assignment.Rhs[i])
			}
		}
		return true
	})
	if assignments["s.conversationCounts"] != "conversations" || assignments["s.messageCounts"] != "messages" {
		t.Fatal("SetCountOwners must install both explicit owner capabilities")
	}

	routerWiring, handlerCalls := 0, 0
	for path, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if method, ok := call.Fun.(*ast.SelectorExpr); ok && method.Sel.Name == "CountReportBuckets" {
				receiver, ok := method.X.(*ast.SelectorExpr)
				key := ""
				if ok {
					key = path + "|" + receiver.Sel.Name
				}
				if _, registered := callSites[key]; !registered {
					t.Fatalf("unregistered count capability call in %s: %s", path, reportCountASTText(t, call))
				}
				want := "s.conversationCounts.CountReportBuckets(ctx, accountID, grouping, buckets)"
				if receiver.Sel.Name == "messageCounts" {
					want = "s.messageCounts.CountReportBuckets(ctx, accountID, messageType, grouping, buckets)"
				}
				if reportCountASTText(t, call) != want {
					t.Fatalf("count capability must receive context, account, grouping and bounds: %s", path)
				}
				callSites[key]++
			}
			if function, ok := call.Fun.(*ast.Ident); ok && function.Name == "countReportBuckets" {
				index := -1
				for i, port := range want {
					if port.ProviderRepositoryFile == path {
						index = i
					}
				}
				if index < 0 || len(call.Args) != 7 || reportCountASTText(t, call.Args[3]) != strconv.Quote(want[index].ProviderTable) || reportCountASTText(t, call.Args[5]) != "grouping" || reportCountASTText(t, call.Args[6]) != "buckets" {
					t.Fatalf("count helper must use a fixed owner table in its registered adapter: %s", path)
				}
				expected := `countReportBuckets(ctx, r.db, accountID, "conversations", "", grouping, buckets)`
				if want[index].Provider == "MSG" {
					expected = `countReportBuckets(ctx, r.db, accountID, "messages", messageType, grouping, buckets)`
				}
				if reportCountASTText(t, call) != expected {
					t.Fatalf("count helper arguments changed in %s", path)
				}
				helperSites[path]++
			}
			if method, ok := call.Fun.(*ast.SelectorExpr); ok && method.Sel.Name == "SetCountOwners" {
				if path != want[0].CompositionFile || reportCountASTText(t, call) != "reportService.SetCountOwners(convRepo, msgRepo)" {
					t.Fatalf("unregistered RPT count-owner composition in %s", path)
				}
				routerWiring++
			}
			if method, ok := call.Fun.(*ast.SelectorExpr); ok && (method.Sel.Name == "GetCountSeries" || method.Sel.Name == "GetCountSeriesWithOffset") {
				if path == "internal/service/report_count_series.go" && reportCountASTText(t, call) == "s.GetCountSeriesWithOffset(ctx, accountID, metric, grouping, filter, 0)" {
					return true
				}
				if path != "internal/handler/report_count_series.go" || reportCountASTText(t, call) != "h.reportService.GetCountSeriesWithOffset(c.Request.Context(), c.GetUint(middleware.ContextAccountID), metric, grouping, filter, offsetSeconds)" {
					t.Fatalf("unregistered or non-contextual count-series service call in %s", path)
				}
				handlerCalls++
			}
			return true
		})
	}
	for site, count := range callSites {
		if count != 1 {
			t.Fatalf("count capability site %s occurs %d times, want exactly one", site, count)
		}
	}
	for site, count := range helperSites {
		if count != 1 {
			t.Fatalf("count helper owner adapter %s occurs %d times, want exactly one", site, count)
		}
	}
	if routerWiring != 1 {
		t.Fatalf("RPT count-owner router composition occurs %d times, want one", routerWiring)
	}
	if handlerCalls != 1 {
		t.Fatalf("RPT count-series handler service call occurs %d times, want one", handlerCalls)
	}

	// The transfer object carries aggregate integers only. Neither local port may
	// expand its result into raw rows or make RPT a foreign-storage reader.
	point, ok := findType("internal/domain/report_count.go", "ReportCountPoint").(*ast.StructType)
	if !ok || len(point.Fields.List) != 2 {
		t.Fatal("count points must contain only value and timestamp")
	}
	for i, field := range point.Fields.List {
		if len(field.Names) != 1 || field.Names[0].Name != []string{"Value", "Timestamp"}[i] || reportCountASTText(t, field.Type) != "int64" {
			t.Fatal("count points must contain only int64 value and timestamp")
		}
	}
	for path, constant := range map[string]string{
		"internal/service/report_count_series.go": "ReportCountMaxBuckets",
		"internal/repository/report_count.go":     "reportCountMaxBuckets",
	} {
		boundFound := false
		ast.Inspect(read(path), func(node ast.Node) bool {
			if value, ok := node.(*ast.ValueSpec); ok && len(value.Names) == 1 && value.Names[0].Name == constant && len(value.Values) == 1 {
				boundFound = reportCountASTText(t, value.Values[0]) == strconv.Itoa(want[0].MaxBuckets)
			}
			return true
		})
		if !boundFound {
			t.Fatalf("%s does not declare registered count bound %d", path, want[0].MaxBuckets)
		}
	}
	for _, path := range []string{want[0].ConsumerFile, want[0].ProviderRepositoryFile, want[1].ProviderRepositoryFile, "internal/repository/report_count.go"} {
		ast.Inspect(read(path), func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if identifier, ok := selector.X.(*ast.Ident); ok && identifier.Name == "domain" {
				switch selector.Sel.Name {
				case "ReportCountOffsetInstantValid", "ReportCountCalendarBounds", "ReportCountMaxOffsetSeconds", "ReportCountGrouping", "ReportCountHour", "ReportCountDay", "ReportCountMonth", "ReportCountYear", "ReportCountBucket", "ReportCountPoint", "MessageTypeIncoming", "MessageTypeOutgoing":
				default:
					t.Fatalf("count capability %s refers to non-count domain data %s", path, selector.Sel.Name)
				}
			}
			if path == want[0].ConsumerFile && selector.Sel.Name == "db" {
				t.Fatal("RPT count orchestration must not access storage")
			}
			return true
		})
	}
}
