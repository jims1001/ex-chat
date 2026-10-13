package database

import (
	"fmt"
	"reflect"
	"sort"

	"gorm.io/gorm"
)

// ModuleIDs returns the architecture's logical modules, including modules that
// have no persisted domain models yet. It does not imply independent runtimes.
func ModuleIDs() []string {
	return []string{"AIC", "API", "AUD", "AUT", "BIL", "CHN", "CON", "CUS", "ENT", "EXT", "IAM", "KB", "MED", "MIG", "MSG", "NTF", "OPS", "ORC", "QLT", "RPT", "RTE", "RTM", "SRH", "SYS", "TEN", "TKT"}
}

// ModuleModels selects only the module's persisted models and its local journal.
// Model instances are fresh so callers cannot mutate the global migration plan.
func ModuleModels(module string) ([]any, error) {
	ids := ModuleIDs()
	i := sort.SearchStrings(ids, module)
	if i == len(ids) || ids[i] != module {
		return nil, fmt.Errorf("unknown module %q", module)
	}
	var models []any
	for _, model := range allModels() {
		name := reflect.TypeOf(model).Elem().Name()
		owner, ok := modelOwners[name]
		if !ok && name != "LocalChangeJournal" {
			return nil, fmt.Errorf("missing migration owner for %s", name)
		}
		if owner == module || name == "LocalChangeJournal" {
			models = append(models, model)
		}
	}
	return models, nil
}

// AutoMigrateModule prepares an isolated module store. GORM's normal migration
// recursively follows associations, which would create other owners' tables.
// Disable that traversal on a private config copy, leaving the combined server
// unchanged. Cross-module IDs remain scalar references, not database constraints.
// This does not move data or split repositories. Owner-local security cleanup
// invalidates obsolete credentials after schema preparation.
func AutoMigrateModule(db *gorm.DB, module string) error {
	models, err := ModuleModels(module)
	if err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("module migration requires a database")
	}
	isolated := db.Session(&gorm.Session{NewDB: true})
	isolated.Config.IgnoreRelationshipsWhenMigrating = true
	isolated.Config.DisableForeignKeyConstraintWhenMigrating = true
	if err := isolated.AutoMigrate(models...); err != nil {
		return err
	}
	return migrateConfirmationSecurity(isolated, module)
}
