package test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The owner storage scanner alone cannot detect a synchronous port cycle. Keep
// this application coordinator's placement and its exact owner-facing surfaces
// executable, in addition to the frozen general dependency/source inventories.
func checkCreationWorkflowBoundary(source string, owner string) error {
	file, err := parser.ParseFile(token.NewFileSet(), "workflow.go", source, 0)
	if err != nil {
		return err
	}
	var failure error
	conCalls, msgCalls := map[string]bool{}, map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		if failure != nil {
			return false
		}
		if sel, ok := node.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "gorm" && sel.Sel.Name == "DB" && owner == "ORC" {
				failure = fmt.Errorf("ORC coordinator must not own a database")
				return false
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if owner == "CON" && (sel.Sel.Name == "CreateCreationWorkflowMessage" || sel.Sel.Name == "CompleteCreationWorkflow" || sel.Sel.Name == "ReconcileCreationWorkflows") {
			failure = fmt.Errorf("CON must not call cross-owner workflow driver")
			return false
		}
		if owner == "ORC" {
			switch sel.Sel.Name {
			case "Exec", "Raw", "Table", "Model", "Transaction", "Begin", "Save", "Update", "Updates", "Create", "Delete", "First", "Find":
				failure = fmt.Errorf("ORC coordinator cannot access owner storage through %s", sel.Sel.Name)
				return false
			}
			if field, ok := sel.X.(*ast.SelectorExpr); ok {
				switch field.Sel.Name {
				case "conversations":
					switch sel.Sel.Name {
					case "ReadCreationWorkflow", "PendingCreationWorkflows", "FinalizeCreationWorkflow", "RejectCreationWorkflow":
						conCalls[sel.Sel.Name] = true
					default:
						failure = fmt.Errorf("undeclared CON workflow port %s", sel.Sel.Name)
					}
				case "messages":
					if sel.Sel.Name != "CreateCreationWorkflowMessage" {
						failure = fmt.Errorf("undeclared MSG workflow port %s", sel.Sel.Name)
					}
					msgCalls[sel.Sel.Name] = true
				}
			}
		}
		return true
	})
	if failure != nil {
		return failure
	}
	if owner == "ORC" && (len(conCalls) != 4 || len(msgCalls) != 1) {
		return fmt.Errorf("coordinator owner ports changed: CON=%v MSG=%v", conCalls, msgCalls)
	}
	return nil
}

func TestCreationWorkflowCoordinatorPreservesOwnerDirection(t *testing.T) {
	for file, owner := range map[string]string{"../internal/repository/conversation_creation_workflow.go": "CON", "../internal/repository/creation_workflow_coordinator.go": "ORC"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkCreationWorkflowBoundary(string(raw), owner); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	raw, err := os.ReadFile("../internal/modruntime/aut_bridge.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "aut_bridge.go", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range file.Imports {
		if strings.Contains(imp.Path.Value, "internal/repository") || strings.Contains(imp.Path.Value, "internal/service") {
			t.Fatal("AUT runtime must not import owner/coordinator implementations")
		}
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		recv := fn.Recv.List[0].Type
		if pointer, ok := recv.(*ast.StarExpr); ok {
			recv = pointer.X
		}
		typ, ok := recv.(*ast.Ident)
		if !ok || typ.Name != "Runtime" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if sel, ok := node.(*ast.SelectorExpr); ok && (sel.Sel.Name == "ReconcileCreationWorkflows" || sel.Sel.Name == "CompleteCreationWorkflow") {
				t.Fatal("creation recovery belongs to outer application composition, never AUT Runtime")
			}
			return true
		})
	}
}

func TestCreationWorkflowBoundaryGuardRejectsMutations(t *testing.T) {
	con, err := os.ReadFile("../internal/repository/conversation_creation_workflow.go")
	if err != nil {
		t.Fatal(err)
	}
	if checkCreationWorkflowBoundary(string(con)+"\nfunc badOwnerCycle(){ messages.CreateCreationWorkflowMessage() }\n", "CON") == nil {
		t.Fatal("guard accepted CON-to-MSG cycle")
	}
	coordinator, err := os.ReadFile("../internal/repository/creation_workflow_coordinator.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutated := range []string{strings.Replace(string(coordinator), "type CreationWorkflowCoordinator struct {", "type CreationWorkflowCoordinator struct {\n db *gorm.DB", 1), string(coordinator) + "\nfunc badStorage(){ owner.Model() }\n"} {
		if checkCreationWorkflowBoundary(mutated, "ORC") == nil {
			t.Fatal("guard accepted coordinator owner storage access")
		}
	}
}
