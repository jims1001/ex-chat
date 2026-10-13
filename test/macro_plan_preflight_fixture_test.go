package test

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Macro execution has five real owners. AUT execution records belong to a sixth
// owner and are asserted by the real-router fixture, never migrated into OPS.
var macroPlanOwners = []string{"OPS", "TEN", "CON", "CUS", "MSG"}

type macroPlanFixture struct {
	stores map[string]*gorm.DB
	repo   *repository.MacroRepository
	con    *repository.ConversationRepository
	label  domain.Label
	team   domain.Team
}

func macroPlanSQLite(t *testing.T, owner string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return db
}

func newMacroPlanFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *macroPlanFixture {
	t.Helper()
	f := &macroPlanFixture{stores: map[string]*gorm.DB{}}
	for _, owner := range macroPlanOwners {
		f.stores[owner] = open(t, owner)
		require.NoError(t, database.AutoMigrateModule(f.stores[owner], owner))
	}
	for _, account := range []uint{91, 92} {
		require.NoError(t, f.stores["TEN"].Create(&domain.Account{ID: account, Name: "Macro plan owner"}).Error)
		require.NoError(t, f.stores["TEN"].Create(&domain.AccountUser{AccountID: account, UserID: account, Role: domain.RoleAgent}).Error)
	}
	f.team = domain.Team{AccountID: 91, Name: "Macro plan team"}
	require.NoError(t, f.stores["TEN"].Create(&f.team).Error)
	f.label = domain.Label{AccountID: 91, Title: "existing-plan-label"}
	require.NoError(t, f.stores["CUS"].Create(&f.label).Error)
	require.NoError(t, f.stores["CUS"].Create(&domain.Label{AccountID: 92, Title: "foreign-plan-label"}).Error)
	f.con = repository.NewConversationRepository(f.stores["CON"])
	f.con.SetAutomationCapture(true)
	msg := repository.NewMessageRepositoryWithConversationPort(f.stores["MSG"], f.con)
	msg.SetAutomationCapture(true)
	f.repo = repository.NewMacroRepositoryWithOwnerPorts(f.stores["OPS"], f.con,
		repository.NewAccountRepository(f.stores["TEN"]), repository.NewTeamRepository(f.stores["TEN"]),
		repository.NewLabelRepositoryWithConversationPort(f.stores["CUS"], f.con), msg)
	// Deliberately no fallback/shared tables: an accidental wrong-owner query fails.
	for _, owner := range macroPlanOwners {
		db := f.stores[owner]
		for table, actual := range map[string]string{"macros": "OPS", "account_users": "TEN", "teams": "TEN", "conversations": "CON", "conversation_labels": "CON", "labels": "CUS", "messages": "MSG", "attachments": "MSG"} {
			require.Equal(t, actual == owner, db.Migrator().HasTable(table), "%s / %s", owner, table)
		}
		require.True(t, db.Migrator().HasTable("local_change_journals"))
		require.False(t, db.Migrator().HasTable("automation_rule_executions"), "AUT is not one of the macro's five owners")
	}
	return f
}

func (f *macroPlanFixture) conversation(t *testing.T, account uint) domain.Conversation {
	t.Helper()
	at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	assignee, team := uint(91), f.team.ID
	c := domain.Conversation{AccountID: account, InboxID: 71, ContactID: 81, Status: "open", Priority: "low", ObjectVersion: 7, AssigneeID: &assignee, TeamID: &team, LastActivityAt: at, CreatedAt: at, UpdatedAt: at}
	require.NoError(t, f.stores["CON"].Create(&c).Error)
	require.NoError(t, f.stores["CON"].Create(&domain.ConversationLabel{ConversationID: c.ID, LabelID: f.label.ID}).Error)
	return c
}
func (f *macroPlanFixture) macro(t *testing.T, raw string) domain.Macro {
	t.Helper()
	m := domain.Macro{AccountID: 91, CreatedBy: 91, Visibility: "global", Name: "Persisted macro plan", Actions: raw}
	require.NoError(t, f.stores["OPS"].Create(&m).Error)
	return m
}
func macroPlanJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
func macroPlanAction(name string, params ...string) map[string]any {
	return map[string]any{"action_name": name, "action_params": params}
}

// Snapshot every physical table, every row and every column, including activity,
// versions, journals, pre-existing rows and unrelated tenant state. Sorting JSON
// rows makes this independent of backend row ordering and primary-key shape.
func macroPlanSnapshot(t *testing.T, stores map[string]*gorm.DB) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for owner, db := range stores {
		tables, err := db.Migrator().GetTables()
		require.NoError(t, err)
		for _, table := range tables {
			var rows []map[string]any
			require.NoError(t, db.Table(table).Find(&rows).Error, "%s / %s", owner, table)
			encoded := make([]string, 0, len(rows))
			for _, row := range rows {
				for key, value := range row {
					switch value := value.(type) {
					case []byte:
						row[key] = string(value)
					case time.Time:
						row[key] = value.UTC().Format(time.RFC3339Nano)
					}
				}
				encoded = append(encoded, macroPlanJSON(t, row))
			}
			sort.Strings(encoded)
			out[owner+"/"+table] = encoded
		}
	}
	return out
}

// Count attempted writes as well as committed changes: rollback cannot disguise
// an interleaved execute-then-reject implementation as a successful preflight.
func macroPlanObserve(t *testing.T, stores map[string]*gorm.DB) map[string]*atomic.Int64 {
	t.Helper()
	counts := map[string]*atomic.Int64{}
	for owner, db := range stores {
		count := &atomic.Int64{}
		counts[owner] = count
		add := func(*gorm.DB) { count.Add(1) }
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("macro_plan_create", add))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("macro_plan_update", add))
		require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("macro_plan_delete", add))
		require.NoError(t, db.Callback().Raw().Before("gorm:raw").Register("macro_plan_raw", func(tx *gorm.DB) {
			query := strings.ToUpper(strings.TrimSpace(tx.Statement.SQL.String()))
			for _, verb := range []string{"INSERT", "UPDATE", "DELETE", "REPLACE", "WITH"} {
				if strings.HasPrefix(query, verb) {
					count.Add(1)
					return
				}
			}
		}))
		t.Cleanup(func() {
			require.NoError(t, db.Callback().Create().Remove("macro_plan_create"))
			require.NoError(t, db.Callback().Update().Remove("macro_plan_update"))
			require.NoError(t, db.Callback().Delete().Remove("macro_plan_delete"))
			require.NoError(t, db.Callback().Raw().Remove("macro_plan_raw"))
		})
	}
	return counts
}
func macroPlanAssertUnchanged(t *testing.T, stores map[string]*gorm.DB, before map[string][]string, counts map[string]*atomic.Int64) {
	t.Helper()
	for owner, count := range counts {
		require.Zero(t, count.Load(), "%s attempted a write", owner)
	}
	require.Equal(t, before, macroPlanSnapshot(t, stores), "rejected plan changed owner state")
}
