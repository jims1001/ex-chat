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

// This is a local, typed MSG -> CHN capability. It is not a remote contract
// and does not compose the standalone module server.
type architectureLocalTextOwnerPort struct {
	Consumer, Provider, Kind, Scope                              string
	Port                                                         string
	Methods                                                      []string
	DefinitionFile, ConsumerFile, ConsumerType, ConsumerField    string
	ConsumerFactory, ProviderFile, ProviderType, ProviderFactory string
	ProviderRepository, ProviderRepositoryMethod                 string
	ProviderRepositoryFile, PreparedPort, PreparedType           string
	CompositionFile, StandaloneFile                              string
}

func validateArchitectureTextOwnerPorts(t *testing.T, sources architectureDependencySources, modules map[string]architectureDependencyModule) {
	t.Helper()
	want := []architectureLocalTextOwnerPort{{
		Consumer: "MSG", Provider: "CHN", Kind: "synchronous", Scope: "local-text-transport-only",
		Port: "channel.TextOwner", Methods: []string{"Ready", "OutboundEnabled", "PrepareText", "PrepareDispatch", "LookupText", "VerifyTwilioStatus", "ParseWhatsAppStatuses", "CheckBinding"},
		DefinitionFile: "internal/channel/text_owner.go", ConsumerFile: "internal/service/channel_outbound_service.go",
		ConsumerType: "ChannelOutboundService", ConsumerField: "owner", ConsumerFactory: "NewChannelOutboundService",
		ProviderFile: "internal/service/channel_text_owner.go", ProviderType: "channelTextOwner", ProviderFactory: "NewChannelTextOwner",
		ProviderRepository: "InboxRepository", ProviderRepositoryMethod: "OutboundConnection", ProviderRepositoryFile: "internal/repository/inbox_outbound.go",
		PreparedPort: "channel.PreparedText", PreparedType: "preparedChannelText", CompositionFile: "internal/router/router.go", StandaloneFile: "cmd/module-server/main.go",
	}}
	if !reflect.DeepEqual(sources.LocalTextOwnerPorts, want) {
		t.Fatalf("text owner-port inventory changed: got %+v, want %+v", sources.LocalTextOwnerPorts, want)
	}
	port := want[0]
	allowed := false
	for _, provider := range modules[port.Consumer].Synchronous {
		allowed = allowed || provider == port.Provider
	}
	if !allowed || sources.RepositoryOwners[port.ProviderRepository] != "CHN" || sources.RepositoryOwners[port.ConsumerType] != "MSG" {
		t.Fatal("text ownership must retain the real synchronous MSG -> CHN edge")
	}
	for path, owner := range map[string]string{port.ConsumerFile: "MSG", port.ProviderFile: "CHN"} {
		count := 0
		for _, scope := range sources.ScanScopes {
			if scope.File == path {
				if scope.Consumer != owner {
					t.Fatalf("%s must retain its %s source owner", path, owner)
				}
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s requires exactly one %s source scope", path, owner)
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
			t.Fatalf("missing text owner source %s", path)
		}
		return files[path]
	}
	typ := func(path, name string) ast.Expr {
		t.Helper()
		var result ast.Expr
		ast.Inspect(read(path), func(n ast.Node) bool {
			if spec, ok := n.(*ast.TypeSpec); ok && spec.Name.Name == name {
				if spec.Assign.IsValid() || result != nil || spec.TypeParams != nil {
					t.Fatalf("text capability %s must be one concrete non-generic definition", name)
				}
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
		t.Helper()
		for _, d := range read(path).Decls {
			f, ok := d.(*ast.FuncDecl)
			if !ok || f.Name.Name != name {
				continue
			}
			if receiver == "" && f.Recv == nil || f.Recv != nil && len(f.Recv.List) == 1 && reportCountASTText(t, f.Recv.List[0].Type) == receiver {
				return f
			}
		}
		t.Fatalf("missing %s.%s in %s", receiver, name, path)
		return nil
	}
	fields := func(path, name string, want []string) {
		t.Helper()
		s, ok := typ(path, name).(*ast.StructType)
		if !ok {
			t.Fatalf("%s must remain a bounded struct", name)
		}
		var got []string
		for _, field := range s.Fields.List {
			if field.Tag != nil || len(field.Names) > 1 {
				t.Fatalf("%s fields must remain explicit and untagged", name)
			}
			text := reportCountASTText(t, field.Type)
			if len(field.Names) == 1 {
				text = field.Names[0].Name + " " + text
			}
			got = append(got, text)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s must retain its bounded owner-safe fields: got %v, want %v", name, got, want)
		}
	}
	fields(port.DefinitionFile, "TextIntent", []string{"AttemptID string", "AccountID uint", "InboxID uint", "RecipientID string", "Body string"})
	fields(port.DefinitionFile, "TextBinding", []string{"ConnectionID uint", "Provider string", "ProviderAccountID string", "SenderID string", "ConfigFingerprint string", "APIVersion string", "CallbackURL string"})
	fields(port.DefinitionFile, "TextSnapshot", []string{"TextIntent", "TextBinding"})
	fields("internal/channel/outbound.go", "Result", []string{"ProviderID string", "Status string", "Disposition string"})
	fields("internal/channel/statuses.go", "StatusEvent", []string{"AttemptID string", "Provider string", "ProviderAccountID string", "SenderID string", "RecipientID string", "ProviderID string", "Status string"})
	fields(port.ConsumerFile, port.ConsumerType, []string{"owner channel.TextOwner", "conversations *repository.ConversationRepository", "messages *repository.MessageRepository"})
	fields(port.ProviderFile, port.ProviderType, []string{"config channel.Config", "transport channel.TextTransport", "inboxes *repository.InboxRepository"})
	if reportCountASTText(t, typ(port.ProviderFile, port.PreparedType)) != "func(context.Context) channel.Result" {
		t.Fatal("prepared text must be an opaque private function, without inspectable credential fields")
	}

	// Validate both sides of every method; a DTO expansion, extra method, store
	// handle, native request, or credential cannot be hidden behind the port.
	signatures := map[string][]string{
		"Ready":                 {"|", "|", "error"},
		"OutboundEnabled":       {"|", "|", "bool"},
		"PrepareText":           {"|", "context.Context", "TextIntent", "|", "TextBinding", "error"},
		"PrepareDispatch":       {"|", "context.Context", "TextSnapshot", "|", "PreparedText", "error"},
		"LookupText":            {"|", "context.Context", "TextSnapshot", "string", "|", "Result", "error"},
		"VerifyTwilioStatus":    {"|", "context.Context", "TextSnapshot", "StatusEvent", "map[string]string", "string", "|", "error"},
		"ParseWhatsAppStatuses": {"|", "uint", "string", "[]byte", "|", "[]StatusEvent", "error"},
		"CheckBinding":          {"|", "context.Context", "TextSnapshot", "|", "error"},
	}
	definition, ok := typ(port.DefinitionFile, "TextOwner").(*ast.InterfaceType)
	if !ok || len(definition.Methods.List) != len(port.Methods) {
		t.Fatal("TextOwner must contain exactly its registered methods")
	}
	for i, method := range definition.Methods.List {
		if len(method.Names) != 1 || method.Names[0].Name != port.Methods[i] {
			t.Fatal("TextOwner cannot embed or add an unregistered capability")
		}
		signature, ok := method.Type.(*ast.FuncType)
		if !ok || !reflect.DeepEqual(reportCountSignature(t, signature), signatures[port.Methods[i]]) {
			t.Fatalf("TextOwner.%s signature changed", port.Methods[i])
		}
		got := reportCountSignature(t, fn(port.ProviderFile, "*"+port.ProviderType, port.Methods[i]).Type)
		for j := range got {
			got[j] = strings.ReplaceAll(got[j], "channel.", "")
		}
		if !reflect.DeepEqual(got, signatures[port.Methods[i]]) {
			t.Fatalf("CHN implementation %s signature changed", port.Methods[i])
		}
	}
	prepared, ok := typ(port.DefinitionFile, "PreparedText").(*ast.InterfaceType)
	if !ok || len(prepared.Methods.List) != 1 || len(prepared.Methods.List[0].Names) != 1 || prepared.Methods.List[0].Names[0].Name != "Send" {
		t.Fatal("prepared text must be an opaque Send-only capability")
	}
	preparedSignature, ok := prepared.Methods.List[0].Type.(*ast.FuncType)
	if !ok || !reflect.DeepEqual(reportCountSignature(t, preparedSignature), []string{"|", "context.Context", "|", "Result"}) {
		t.Fatal("prepared text must only send with context and return a credential-free Result")
	}
	for _, check := range []struct {
		path, receiver, name string
		signature            []string
	}{
		{port.ConsumerFile, "", port.ConsumerFactory, []string{"|", "channel.TextOwner", "*repository.ConversationRepository", "*repository.MessageRepository", "|", "*ChannelOutboundService"}},
		{port.ProviderFile, "", port.ProviderFactory, []string{"|", "channel.Config", "channel.TextTransport", "*repository.InboxRepository", "|", "channel.TextOwner"}},
		{port.ProviderFile, "preparedChannelText", "Send", []string{"|", "context.Context", "|", "channel.Result"}},
		{port.ProviderRepositoryFile, "*InboxRepository", "OutboundConnection", []string{"|", "context.Context", "uint", "uint", "|", "*domain.ChannelConnection", "error"}},
	} {
		if !reflect.DeepEqual(reportCountSignature(t, fn(check.path, check.receiver, check.name).Type), check.signature) {
			t.Fatalf("text owner boundary signature changed for %s", check.name)
		}
	}
	for _, check := range []struct{ path, receiver, name, body string }{
		{port.ConsumerFile, "", port.ConsumerFactory, "{\n\treturn &ChannelOutboundService{owner, conversations, messages}\n}"},
		{port.ProviderFile, "", port.ProviderFactory, "{\n\tif transport == nil {\n\t\ttransport = channel.NewHTTPTextTransport(nil)\n\t}\n\treturn &channelTextOwner{cfg, transport, inboxes}\n}"},
		{port.ProviderFile, "preparedChannelText", "Send", "{\n\treturn p(ctx)\n}"},
		{port.ProviderFile, "*channelTextOwner", "PrepareDispatch", "{\n\tc, err := s.connectionBinding(ctx, a)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\treq, err := s.request(a, c)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\ttransport := s.transport\n\treturn preparedChannelText(func(ctx context.Context) channel.Result {\n\t\treturn transport.SendText(ctx, req)\n\t}), nil\n}"},
	} {
		if reportCountASTText(t, fn(check.path, check.receiver, check.name).Body) != check.body {
			t.Fatalf("%s must preserve explicit owner composition and opaque dispatch", check.name)
		}
	}

	// Import allowlists rule out native provider, signature, crypto, reflection,
	// network, or DB packages being smuggled into MSG through a renamed import.
	const module = "github.com/OracleBetX-Projects/ex-chat/internal/"
	for path, allowed := range map[string][]string{
		port.DefinitionFile: {"context"},
		port.ConsumerFile:   {"context", "errors", "fmt", "net/url", "strings", "time", "unicode/utf8", module + "channel", module + "domain", module + "repository", "github.com/google/uuid", "gorm.io/gorm"},
		port.ProviderFile:   {"context", "fmt", "strings", "unicode/utf8", module + "channel", module + "domain", module + "repository", "github.com/twilio/twilio-go/client"},
	} {
		for _, imported := range read(path).Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, permitted := range allowed {
				found = found || value == permitted
			}
			if !found || imported.Name != nil {
				t.Fatalf("unregistered text owner import %s in %s", value, path)
			}
		}
	}
	consumerChannel := map[string]bool{}
	for _, name := range []string{"TextOwner", "TextIntent", "TextBinding", "TextSnapshot", "PreparedText", "StableKey", "ParseTwilioStatus", "ErrNotConfigured", "ErrUnsupported", "ErrInvalid", "ErrUnauthorized", "ErrProvider"} {
		consumerChannel[name] = true
	}
	for _, path := range []string{port.ConsumerFile, port.ProviderFile} {
		ast.Inspect(read(path), func(n ast.Node) bool {
			if declaration, ok := n.(*ast.GenDecl); ok && declaration.Tok == token.VAR {
				// A package-level owner handle would bypass the explicit fields.
				for _, top := range read(path).Decls {
					if top == declaration {
						t.Fatalf("text owner handles must not be package globals in %s", path)
					}
				}
			}
			selector, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			base := reportCountASTText(t, selector.X)
			name := selector.Sel.Name
			if path == port.ConsumerFile {
				if base == "channel" && !consumerChannel[name] || base == "gorm" && name != "ErrRecordNotFound" && name != "ErrInvalidData" {
					t.Fatalf("MSG text consumer gained a provider/config/DB capability: %s", reportCountASTText(t, selector))
				}
				for _, forbidden := range []string{"Config", "TextRequest", "TextTransport", "ChannelConnection", "InboxRepository", "SecretCiphertext", "Credential", "Key", "Open", "Seal", "DB", "db", "config", "transport", "inboxes", "NewInboxRepository", "NewChannelTextOwner"} {
					if name == forbidden {
						t.Fatalf("MSG text consumer must not retain CHN credentials or storage: %s", reportCountASTText(t, selector))
					}
				}
			} else if base == "repository" && name != "InboxRepository" || path == port.ProviderFile && base == "domain" && name != "ChannelConnection" {
				t.Fatalf("CHN text owner must not load MSG state: %s", reportCountASTText(t, selector))
			}
			return true
		})
	}

	// Inventory real call expressions and their owning functions, not graph
	// labels. Method values/expressions cannot move a credential read elsewhere.
	type callSite struct{ path, function, expression string }
	expected := map[callSite]int{}
	add := func(path, function, expression string) { expected[callSite{path, function, expression}]++ }
	add(port.ConsumerFile, "ready", "s.owner.Ready()")
	add(port.ConsumerFile, "prepare", "s.owner.OutboundEnabled()")
	add(port.ConsumerFile, "DispatchOne", "s.owner.OutboundEnabled()")
	add(port.ConsumerFile, "prepare", "s.owner.PrepareText(ctx, channel.TextIntent{AttemptID: id, AccountID: msg.AccountID, InboxID: target.InboxID, RecipientID: target.RecipientID, Body: msg.Content})")
	add(port.ConsumerFile, "sendBinding", "s.owner.PrepareDispatch(ctx, outboundTextSnapshot(a))")
	add(port.ConsumerFile, "ReconcileText", "s.owner.LookupText(ctx, outboundTextSnapshot(a), a.ProviderMessageID)")
	add(port.ConsumerFile, "IngestTwilioStatus", "s.owner.VerifyTwilioStatus(ctx, outboundTextSnapshot(a), event, params, signature)")
	add(port.ConsumerFile, "IngestWhatsAppStatus", "s.owner.ParseWhatsAppStatuses(accountID, signature, raw)")
	add(port.ConsumerFile, "IngestWhatsAppStatus", "s.owner.CheckBinding(ctx, outboundTextSnapshot(a))")
	add(port.ConsumerFile, "DispatchOne", "prepared.Send(ctx)")
	add(port.ProviderFile, "PrepareText", "s.inboxes.OutboundConnection(ctx, intent.AccountID, intent.InboxID)")
	add(port.ProviderFile, "connectionBinding", "s.inboxes.OutboundConnection(ctx, a.AccountID, a.InboxID)")
	add(port.ProviderFile, "PrepareDispatch", "transport.SendText(ctx, req)")
	add(port.ProviderFile, "LookupText", "s.transport.LookupText(ctx, req, providerID)")
	add(port.ProviderFile, "ParseWhatsAppStatuses", "channel.ParseWhatsAppStatuses(raw)")
	add(port.CompositionFile, "SetupRouterWithOptions", "service.NewChannelTextOwner(channelConfig, options.ChannelTextTransport, inboxRepo)")
	add(port.CompositionFile, "SetupRouterWithOptions", "service.NewChannelOutboundService(channelTextOwner, convRepo, msgRepo)")
	tracked := map[string]bool{}
	for _, name := range []string{"OutboundConnection", "PrepareText", "PrepareDispatch", "LookupText", "VerifyTwilioStatus", "ParseWhatsAppStatuses", "CheckBinding", "SendText", port.ProviderFactory, port.ConsumerFactory} {
		tracked[name] = true
	}
	for path, file := range files {
		for _, declaration := range file.Decls {
			function := ""
			if f, ok := declaration.(*ast.FuncDecl); ok {
				function = f.Name.Name
				if f.Recv != nil {
					receiver := strings.TrimPrefix(reportCountASTText(t, f.Recv.List[0].Type), "*")
					if receiver == port.ConsumerType && path != port.ConsumerFile || (receiver == port.ProviderType || receiver == port.PreparedType) && path != port.ProviderFile {
						t.Fatalf("unregistered split text-owner receiver %s in %s", receiver, path)
					}
					if receiver == port.PreparedType && function != "Send" {
						t.Fatal("prepared text must not expose anything except Send")
					}
					if receiver == port.ProviderType && ast.IsExported(function) && signatures[function] == nil {
						t.Fatalf("CHN text owner must not expose an unregistered method %s", function)
					}
					if strings.HasPrefix(path, "internal/channel/") {
						for _, dto := range []string{"TextIntent", "TextBinding", "TextSnapshot", "Result", "StatusEvent"} {
							if receiver == dto {
								t.Fatalf("bounded text DTO %s must not gain behavior", dto)
							}
						}
					}
				}
			}
			called := map[ast.Expr]bool{}
			ast.Inspect(declaration, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				called[call.Fun] = true
				name, base := "", ""
				switch value := call.Fun.(type) {
				case *ast.SelectorExpr:
					name, base = value.Sel.Name, reportCountASTText(t, value.X)
				case *ast.Ident:
					name = value.Name
				}
				if tracked[name] || path == port.ConsumerFile && (base == "s.owner" || base == "prepared" && name == "Send") {
					site := callSite{path, function, reportCountASTText(t, call)}
					if expected[site] == 0 {
						t.Fatalf("unregistered text capability/credential/transport call: %+v", site)
					}
					expected[site]--
				}
				return true
			})
			ast.Inspect(declaration, func(n ast.Node) bool {
				if selector, ok := n.(*ast.SelectorExpr); ok && (tracked[selector.Sel.Name] || path == port.ConsumerFile && reportCountASTText(t, selector.X) == "s.owner") && !called[selector] {
					t.Fatalf("text owner method must be called at its registered site, not extracted: %s in %s", reportCountASTText(t, selector), path)
				}
				if path == port.StandaloneFile {
					if id, ok := n.(*ast.Ident); ok && (id.Name == port.ConsumerFactory || id.Name == port.ProviderFactory || id.Name == "TextOwner" || id.Name == "PreparedText" || id.Name == port.ConsumerType) {
						t.Fatal("standalone module-server must not imply text-owner composition")
					}
				}
				return true
			})
		}
	}
	for site, remaining := range expected {
		if remaining != 0 {
			t.Fatalf("missing registered text owner call: %+v", site)
		}
	}
	// Keep actual composition and the MSG dispatch fence explicit in the AST.
	composition := fn(port.CompositionFile, "", "SetupRouterWithOptions").Body.List
	wired := false
	for i := 1; i < len(composition); i++ {
		if reportCountASTText(t, composition[i-1]) == "channelTextOwner := service.NewChannelTextOwner(channelConfig, options.ChannelTextTransport, inboxRepo)" && reportCountASTText(t, composition[i]) == "channelOutbound := service.NewChannelOutboundService(channelTextOwner, convRepo, msgRepo)" {
			wired = true
		}
	}
	if !wired {
		t.Fatal("combined router must compose CHN credentials into the typed MSG owner port")
	}
	dispatch := fn(port.ConsumerFile, "*ChannelOutboundService", "DispatchOne").Body.List
	prepareAt, fenceAt, sendAt := -1, -1, -1
	for i, statement := range dispatch {
		switch reportCountASTText(t, statement) {
		case "prepared, err := s.sendBinding(ctx, a)":
			prepareAt = i
		case "a, err = s.messages.StartOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, time.Now().UTC())":
			fenceAt = i
		case "result := prepared.Send(ctx)":
			sendAt = i
		}
	}
	if prepareAt < 0 || fenceAt <= prepareAt || sendAt <= fenceAt {
		t.Fatal("MSG must prepare with CHN, persist its dispatch fence, then invoke opaque Send")
	}
}
