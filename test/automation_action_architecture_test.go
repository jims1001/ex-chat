package test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func checkAutomationActionOwnerSource(source, owner string, models map[string]string, edges map[string][]string) error {
	file, err := parser.ParseFile(token.NewFileSet(), "owner.go", source, 0)
	if err != nil {
		return err
	}
	var failure error
	ast.Inspect(file, func(node ast.Node) bool {
		if failure != nil {
			return false
		}
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "domain" {
				if target, exists := models[selector.Sel.Name]; exists && target != owner {
					failure = fmt.Errorf("%s source references foreign stored model %s owned by %s", owner, selector.Sel.Name, target)
					return false
				}
			}
		}
		if index, ok := node.(*ast.IndexExpr); ok {
			if selector, ok := index.X.(*ast.SelectorExpr); ok && selector.Sel.Name == "Dependencies" {
				if literal, ok := index.Index.(*ast.BasicLit); ok {
					target, _ := strconv.Unquote(literal.Value)
					allowed := false
					for _, edge := range edges[owner] {
						allowed = allowed || edge == target
					}
					if !allowed {
						failure = fmt.Errorf("undeclared %s -> %s synchronous dependency", owner, target)
						return false
					}
				} else if owner != "AUT" {
					failure = fmt.Errorf("dynamic owner dependency")
					return false
				}
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "newModuleClient" && len(call.Args) > 0 {
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				failure = fmt.Errorf("dynamic module caller")
				return false
			}
			value, _ := strconv.Unquote(literal.Value)
			if value != owner {
				failure = fmt.Errorf("caller %s mismatches %s", value, owner)
				return false
			}
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "Raw" || selector.Sel.Name == "Exec") {
			// The only raw SQL needed here is a PostgreSQL owner-local title lock.
			if owner != "CUS" || len(call.Args) != 2 {
				failure = fmt.Errorf("unapproved raw owner SQL")
				return false
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Value != `"SELECT pg_advisory_xact_lock(?)"` {
				failure = fmt.Errorf("unapproved raw owner SQL")
				return false
			}
		}
		return true
	})
	return failure
}
func TestAutomationActionSourceOwnershipAndExactContracts(t *testing.T) {
	raw, err := os.ReadFile("../architecture/automation_action_sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version         string                                       `json:"version"`
		DefaultMode     string                                       `json:"defaultMode"`
		Sources         map[string]string                            `json:"sources"`
		NewContracts    []struct{ Owner, Kind, Name, Source string } `json:"newContracts"`
		OwnerDirections map[string][]string                          `json:"ownerDirections"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version != "automation-actions-v1" || manifest.DefaultMode != "legacy" || len(manifest.Sources) != 15 || len(manifest.NewContracts) != 6 {
		t.Fatal("action source metadata changed")
	}
	modelsRaw, err := os.ReadFile("../internal/database/model_owners.go")
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]string{}
	for _, match := range regexp.MustCompile(`"(\w+)":\s*"(\w+)"`).FindAllStringSubmatch(string(modelsRaw), -1) {
		models[match[1]] = match[2]
	}
	for path, owner := range manifest.Sources {
		source, err := os.ReadFile("../" + path)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkAutomationActionOwnerSource(string(source), owner, models, manifest.OwnerDirections); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	registryRaw, _ := os.ReadFile("../architecture/module_contracts.json")
	var registry moduleContractRegistry
	if json.Unmarshal(registryRaw, &registry) != nil {
		t.Fatal("registry invalid")
	}
	got := []string{}
	for _, contract := range manifest.NewContracts {
		if manifest.Sources[contract.Source] != contract.Owner {
			t.Fatal("contract source ownership mismatch")
		}
		source, err := os.ReadFile("../" + contract.Source)
		if err != nil {
			t.Fatal(err)
		}
		registration := "RegisterCommand"
		if contract.Kind == "query" {
			registration = "RegisterQuery"
		} else if contract.Kind != "command" {
			t.Fatal("unknown action contract kind")
		}
		if !strings.Contains(string(source), registration+`("`+contract.Name+`"`) {
			t.Fatalf("missing source registration %s", contract.Name)
		}
		declared := false
		for _, module := range registry.Modules {
			if module.Name == contract.Owner {
				names := module.Commands
				if contract.Kind == "query" {
					names = module.Queries
				}
				for _, name := range names {
					declared = declared || name == contract.Name
				}
			}
		}
		if !declared {
			t.Fatalf("missing declared contract %s", contract.Name)
		}
		got = append(got, contract.Name)
	}
	sort.Strings(got)
	want := []string{"con.automation.apply.v1", "con.automation.message.prepare.v1", "con.automation.message_authorization.get.v1", "cus.automation.label.resolve.v1", "cus.automation.number_definition.get.v1", "msg.automation.message.create.v1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("contracts=%v", got)
	}
	for _, mutation := range []struct{ owner, source string }{{"AUT", `package bad;func bad(){var x domain.Conversation; db.Save(&x)}`}, {"CON", `package bad;func bad(){dep:=r.Config.Dependencies["MSG"];_ = dep}`}, {"MSG", `package bad;func bad(){db.Exec("DELETE FROM conversations")}`}, {"CUS", `package bad;func bad(){newModuleClient("AUT", d, v, t)}`}} {
		if checkAutomationActionOwnerSource(mutation.source, mutation.owner, models, manifest.OwnerDirections) == nil {
			t.Fatalf("guard accepted mutation %s", mutation.source)
		}
	}
}
