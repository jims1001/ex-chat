package database

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func moduleTestDB(t *testing.T, driver string) *gorm.DB {
	t.Helper()
	var dialector gorm.Dialector
	if driver == "postgres" {
		dsn := os.Getenv("DB_DSN")
		if dsn == "" {
			t.Skip("PostgreSQL requires DB_DSN; make test-docker supplies an isolated test instance")
		}
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal("invalid PostgreSQL test configuration")
		}
		admin := stdlib.OpenDB(*cfg)
		schema := "module_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
			admin.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
				t.Error(err)
			}
			admin.Close()
		})
		cfg.RuntimeParams["search_path"] = schema
		conn := stdlib.OpenDB(*cfg)
		t.Cleanup(func() { conn.Close() })
		dialector = postgres.New(postgres.Config{Conn: conn})
	} else {
		dialector = sqlite.Open(filepath.Join(t.TempDir(), "module.db"))
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if driver == "sqlite" {
		conn, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
	}
	return db
}

func TestModuleMigrationOwnershipMatchesArchitecture(t *testing.T) {
	raw, err := os.ReadFile("../../Chatwoot完整文档/验收证据/ARCH-02-领域对象所有权索引.md")
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		columns := strings.Split(line, "|")
		if len(columns) == 8 {
			owners[strings.TrimSpace(columns[2])] = strings.TrimSpace(columns[4])
		}
	}
	seen := map[string]bool{}
	for _, module := range ModuleIDs() {
		models, err := ModuleModels(module)
		if err != nil {
			t.Fatal(err)
		}
		journalCount := 0
		for _, model := range models {
			name := reflect.TypeOf(model).Elem().Name()
			if name == "LocalChangeJournal" {
				journalCount++
				continue
			}
			if owners[name] != module || seen[name] {
				t.Fatalf("invalid or duplicate owner: %s -> %s (architecture %s)", name, module, owners[name])
			}
			seen[name] = true
		}
		if journalCount != 1 {
			t.Fatalf("%s requires exactly one local journal", module)
		}
	}
	if len(seen) != len(allModels())-1 || len(seen) != len(modelOwners) {
		t.Fatal("combined and isolated migration models differ")
	}
}

func TestModuleMigrationsAreIsolatedAndRepeatable(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			for _, module := range ModuleIDs() {
				t.Run(module, func(t *testing.T) {
					db := moduleTestDB(t, driver)
					models, err := ModuleModels(module)
					if err != nil {
						t.Fatal(err)
					}
					var want []string
					for _, model := range models {
						stmt := &gorm.Statement{DB: db}
						if err := stmt.Parse(model); err != nil {
							t.Fatal(err)
						}
						want = append(want, stmt.Schema.Table)
					}
					sort.Strings(want)
					if err := AutoMigrateModule(db, module); err != nil {
						t.Fatal(err)
					}
					journal := domain.LocalChangeJournal{ChangeID: "retained", SourceModule: module, AccountID: 1, ActorID: 1, ActorType: "system", Action: "create"}
					if err := db.Create(&journal).Error; err != nil {
						t.Fatal(err)
					}
					if err := AutoMigrateModule(db, module); err != nil {
						t.Fatalf("repeat migration: %v", err)
					}
					var retained domain.LocalChangeJournal
					if err := db.Where("change_id = ?", "retained").First(&retained).Error; err != nil || retained.SourceModule != module {
						t.Fatalf("existing journal not retained: %v", err)
					}
					got, err := db.Migrator().GetTables()
					if err != nil {
						t.Fatal(err)
					}
					var actual []string
					for _, table := range got {
						if !strings.HasPrefix(table, "sqlite_") {
							actual = append(actual, table)
						}
					}
					sort.Strings(actual)
					if !reflect.DeepEqual(actual, want) {
						t.Fatalf("module %s leaked tables: got %v, want %v", module, actual, want)
					}
					if db.Config.IgnoreRelationshipsWhenMigrating || db.Config.DisableForeignKeyConstraintWhenMigrating {
						t.Fatal("isolated migration changed caller configuration")
					}
				})
			}
		})
	}
}

func TestModuleMigrationRejectsInvalidInputBeforeDDL(t *testing.T) {
	db := moduleTestDB(t, "sqlite")
	for _, module := range []string{"", "med", "UNKNOWN", "MED,TEN", "../TEN"} {
		if err := AutoMigrateModule(db, module); err == nil {
			t.Fatalf("accepted invalid module %q", module)
		}
	}
	tables, err := db.Migrator().GetTables()
	if err != nil || len(tables) != 0 {
		t.Fatalf("invalid selection changed schema: %v %v", tables, err)
	}
	if err := AutoMigrateModule(nil, "MED"); err == nil {
		t.Fatal("nil database accepted")
	}
}

func ExampleModuleModels() {
	models, _ := ModuleModels("MED")
	for _, model := range models {
		fmt.Println(reflect.TypeOf(model).Elem().Name())
	}
	// Output:
	// LocalChangeJournal
	// MediaAsset
	// UploadSession
	// SecurityScan
	// AccessGrant
}
