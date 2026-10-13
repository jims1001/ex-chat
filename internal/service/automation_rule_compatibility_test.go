package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/modruntime"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type compatibilitySQLRecorder struct {
	sync.Mutex
	sql []string
}

func (r *compatibilitySQLRecorder) LogMode(logger.LogLevel) logger.Interface { return r }
func (*compatibilitySQLRecorder) Info(context.Context, string, ...any)       {}
func (*compatibilitySQLRecorder) Warn(context.Context, string, ...any)       {}
func (*compatibilitySQLRecorder) Error(context.Context, string, ...any)      {}
func (r *compatibilitySQLRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	q, _ := fc()
	r.Lock()
	defer r.Unlock()
	r.sql = append(r.sql, q)
}
func (r *compatibilitySQLRecorder) reset() { r.Lock(); defer r.Unlock(); r.sql = nil }
func (r *compatibilitySQLRecorder) queries() []string {
	r.Lock()
	defer r.Unlock()
	return append([]string(nil), r.sql...)
}

func compatibilityTestDB(t *testing.T) (*gorm.DB, string, *compatibilitySQLRecorder) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.sqlite")
	recorder := &compatibilitySQLRecorder{}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: recorder, IgnoreRelationshipsWhenMigrating: true})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, db.AutoMigrate(&domain.AutomationRule{}, &domain.AutomationRuleVersion{}))
	return db, path, recorder
}
func compatibilityTestRule(id, account uint, active bool) domain.AutomationRule {
	return domain.AutomationRule{ID: id, AccountID: account, Name: "NEVER-LOAD-RULE-NAME", Description: "NEVER-LOAD-DESCRIPTION", EventName: "conversation_created", Conditions: `[{"key":"status","operator":"is","value":"open"}]`, Actions: `[{"action_name":"change_priority","action_params":"urgent"}]`, Active: active, UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}
func compatibilityTestProjector(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
	out := domain.AutomationCompatibilityRule{ID: strconv.FormatUint(uint64(in.Rule.ID), 10), AccountID: strconv.FormatUint(uint64(in.Rule.AccountID), 10), Active: in.Rule.Active, Event: "conversation_created", UpdatedAt: in.Rule.UpdatedAt, Complete: true, Sources: []domain.AutomationCompatibilitySource{{Source: "saved", Event: "conversation_created", Complete: true, Issues: []domain.AutomationCompatibilityIssue{}}}}
	if in.Frozen != nil {
		v := strconv.FormatUint(uint64(in.Frozen.Version), 10)
		out.LatestVersion = &v
	}
	return out
}
func compatibilityTestService(db *gorm.DB) *AutomationService {
	return &AutomationService{db: db, compatibilityProjector: compatibilityTestProjector}
}
func compatibilityTestOptions() AutomationCompatibilityOptions {
	return AutomationCompatibilityOptions{Target: domain.AutomationCompatibilityTarget}
}

func TestAutomationCompatibilityReadsOnlyAUTAndNeverWrites(t *testing.T) {
	db, _, recorder := compatibilityTestDB(t)
	rules := []domain.AutomationRule{compatibilityTestRule(1, 91, true), compatibilityTestRule(2, 91, false), compatibilityTestRule(3, 92, true)}
	require.NoError(t, db.Create(&rules).Error)
	versions := []domain.AutomationRuleVersion{{AccountID: 91, RuleID: 1, Version: 1, EventName: "conversation_created", Conditions: "[]", Actions: "[]"}, {AccountID: 91, RuleID: 1, Version: 7, EventName: "conversation_resolved", Conditions: `[{"key":"status","value":"resolved"}]`, Actions: `[{"action_name":"change_priority","action_params":["low"]}]`}, {AccountID: 92, RuleID: 1, Version: 999, EventName: "FOREIGN-SECRET", Conditions: "FOREIGN-SECRET", Actions: "FOREIGN-SECRET"}}
	require.NoError(t, db.Create(&versions).Error)
	for _, table := range []string{"local_change_journals", "automation_rule_executions", "automation_mode_claims"} {
		require.NoError(t, db.Exec("CREATE TABLE "+table+" (id INTEGER PRIMARY KEY, value TEXT)").Error)
		require.NoError(t, db.Exec("INSERT INTO "+table+" (id,value) VALUES (1,'sentinel')").Error)
	}
	require.NoError(t, db.Exec("PRAGMA query_only=ON").Error)
	var input []domain.AutomationCompatibilityInput
	s := compatibilityTestService(db)
	s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
		input = append(input, in)
		return compatibilityTestProjector(in)
	}
	for _, callback := range []func() error{func() error {
		return db.Callback().Create().Before("gorm:create").Register("inventory_no_create", func(tx *gorm.DB) { tx.AddError(errors.New("writes forbidden")) })
	}, func() error {
		return db.Callback().Update().Before("gorm:update").Register("inventory_no_update", func(tx *gorm.DB) { tx.AddError(errors.New("writes forbidden")) })
	}, func() error {
		return db.Callback().Delete().Before("gorm:delete").Register("inventory_no_delete", func(tx *gorm.DB) { tx.AddError(errors.New("writes forbidden")) })
	}} {
		require.NoError(t, callback())
	}
	recorder.reset()
	page, err := s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.NoError(t, err)
	require.Len(t, page.Rules, 2)
	require.True(t, page.PageExhausted)
	require.True(t, page.Complete)
	require.Empty(t, page.NextCursor)
	require.Equal(t, "all", page.Filters.Active)
	require.Equal(t, "saved_account_page", page.SourceKind)
	require.False(t, page.CollectionSnapshot)
	require.Contains(t, page.LiveCollectionCaveat, "between pages")
	require.Equal(t, 1, page.ActiveCounts.Active)
	require.Equal(t, 1, page.ActiveCounts.Inactive)
	input = compatibilityFinalInputs(input)
	require.Len(t, input, 2)
	require.Equal(t, rules[0].Conditions, input[0].Rule.Conditions)
	require.Equal(t, rules[0].Actions, input[0].Rule.Actions)
	require.EqualValues(t, 7, input[0].Frozen.Version)
	require.Equal(t, versions[1].Actions, input[0].Frozen.Actions)
	require.Nil(t, input[0].Preview)
	require.Empty(t, input[0].Rule.Name)
	require.Empty(t, input[0].Rule.Description)
	require.Nil(t, input[1].Frozen)
	require.NotNil(t, input[1].Preview)
	require.Zero(t, input[1].Preview.Version)
	require.True(t, input[1].Preview.CreatedAt.IsZero())
	require.Equal(t, previewAutomationRule(rules[1]).Actions, input[1].Preview.Actions)
	for _, q := range recorder.queries() {
		require.True(t, strings.HasPrefix(q, "SELECT"), q)
		require.NotContains(t, q, "SELECT *")
		require.NotContains(t, q, "NEVER-LOAD")
		require.NotContains(t, q, "FOREIGN-SECRET")
		require.True(t, strings.Contains(q, "automation_rules") || strings.Contains(q, "automation_rule_versions"), q)
		for _, foreign := range []string{"conversations", "contacts", "teams", "messages", "local_change_journals", "automation_rule_executions", "automation_mode_claims"} {
			require.NotContains(t, q, foreign)
		}
	}
	for _, table := range []string{"local_change_journals", "automation_rule_executions", "automation_mode_claims"} {
		var value string
		require.NoError(t, db.Table(table).Select("value").Scan(&value).Error)
		require.Equal(t, "sentinel", value)
	}
	var versionsAfter int64
	require.NoError(t, db.Model(&domain.AutomationRuleVersion{}).Count(&versionsAfter).Error)
	require.EqualValues(t, len(versions), versionsAfter)
	require.False(t, s.durable)
}

func TestAutomationCompatibilityPagingHorizonBindingsAndLiveChanges(t *testing.T) {
	db, _, _ := compatibilityTestDB(t)
	var rules []domain.AutomationRule
	for id := uint(1); id <= 105; id++ {
		rules = append(rules, compatibilityTestRule(id, 91, id%2 == 0))
	}
	rules = append(rules, compatibilityTestRule(201, 92, true))
	require.NoError(t, db.Create(&rules).Error)
	s := compatibilityTestService(db)
	options := compatibilityTestOptions()
	first, err := s.ListRuleCompatibility(context.Background(), 91, options)
	require.NoError(t, err)
	require.Len(t, first.Rules, 100)
	require.False(t, first.PageExhausted)
	require.Equal(t, "105", first.Horizon)
	require.EqualValues(t, 5, first.UninspectedRuleCount)
	for i, rule := range first.Rules {
		require.Equal(t, strconv.Itoa(i+1), rule.ID)
	}
	for _, mutate := range []func(*AutomationCompatibilityOptions){func(o *AutomationCompatibilityOptions) { o.Active = "true" }, func(o *AutomationCompatibilityOptions) { o.RuleID = "1" }, func(o *AutomationCompatibilityOptions) { o.Target = "other" }} {
		o := options
		o.Cursor = first.NextCursor
		mutate(&o)
		_, err := s.ListRuleCompatibility(context.Background(), 91, o)
		require.ErrorIs(t, err, ErrAutomationCompatibilityQuery)
	}
	o := options
	o.Cursor = first.NextCursor
	_, err = s.ListRuleCompatibility(context.Background(), 92, o)
	require.ErrorIs(t, err, ErrAutomationCompatibilityQuery)
	raw, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
	require.NoError(t, err)
	for _, bad := range []string{strings.Replace(string(raw), domain.AutomationCompatibilityRevision, "other", 1), strings.Replace(string(raw), domain.AutomationCompatibilitySchema, "other", 1), `{"schema":"ignored",` + string(raw)[1:], string(raw) + ` {}`, `{"unknown":1,` + string(raw)[1:]} {
		o := options
		o.Cursor = base64.RawURLEncoding.EncodeToString([]byte(bad))
		_, err = s.ListRuleCompatibility(context.Background(), 91, o)
		require.ErrorIs(t, err, ErrAutomationCompatibilityQuery)
	}
	require.NoError(t, db.Delete(&domain.AutomationRule{}, 102).Error)
	require.NoError(t, db.Model(&domain.AutomationRule{}).Where("id = ?", 103).Update("active", true).Error)
	require.NoError(t, db.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: 104, Version: 8, EventName: "conversation_created", Conditions: "[]", Actions: "[]"}).Error)
	newer := compatibilityTestRule(300, 91, true)
	require.NoError(t, db.Create(&newer).Error)
	o = options
	o.Cursor = first.NextCursor
	second, err := s.ListRuleCompatibility(context.Background(), 91, o)
	require.NoError(t, err)
	require.Equal(t, []string{"101", "103", "104", "105"}, []string{second.Rules[0].ID, second.Rules[1].ID, second.Rules[2].ID, second.Rules[3].ID})
	require.True(t, second.PageExhausted)
	require.Empty(t, second.NextCursor)
	require.EqualValues(t, 0, second.UninspectedRuleCount)
	require.True(t, second.Rules[1].Active)
	require.Equal(t, "8", *second.Rules[2].LatestVersion)
	for _, active := range []string{"true", "false"} {
		o := options
		o.Active = active
		p, err := s.ListRuleCompatibility(context.Background(), 91, o)
		require.NoError(t, err)
		for _, rule := range p.Rules {
			require.Equal(t, active == "true", rule.Active)
		}
	}
	o = options
	o.RuleID = "000104"
	one, err := s.ListRuleCompatibility(context.Background(), 91, o)
	require.NoError(t, err)
	require.Len(t, one.Rules, 1)
	require.Equal(t, "104", one.Filters.RuleID)
}

func TestAutomationCompatibilityGuardedSQLBytesAndPreviewBounds(t *testing.T) {
	for _, delta := range []int{-1, 0, 1} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			db, _, recorder := compatibilityTestDB(t)
			size := domain.AutomationCompatibilityMaxBytes + delta
			// Multibyte text plus a NUL: SQLite text length would undercount both cases.
			payload := strings.Repeat("界", size/3) + strings.Repeat("x", size%3)
			payload = payload[:size-1] + "\x00"
			saved := compatibilityTestRule(1, 91, true)
			saved.Conditions = payload
			saved.Actions = payload
			frozenRule := compatibilityTestRule(2, 91, true)
			require.NoError(t, db.Create(&[]domain.AutomationRule{saved, frozenRule}).Error)
			require.NoError(t, db.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: 2, Version: 1, EventName: "conversation_created", Conditions: payload, Actions: payload}).Error)
			var input []domain.AutomationCompatibilityInput
			s := compatibilityTestService(db)
			s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
				input = append(input, in)
				return compatibilityTestProjector(in)
			}
			recorder.reset()
			_, err := s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
			require.NoError(t, err)
			input = compatibilityFinalInputs(input)
			require.Len(t, input, 2)
			require.Equal(t, size, input[0].SavedConditionBytes)
			require.Equal(t, size, input[0].SavedActionBytes)
			require.Equal(t, size, input[1].EffectiveConditionBytes)
			require.Equal(t, size, input[1].EffectiveActionBytes)
			if delta > 0 {
				require.Empty(t, input[0].Rule.Conditions)
				require.Empty(t, input[0].Rule.Actions)
				require.Nil(t, input[0].Preview)
				require.Empty(t, input[1].Frozen.Actions)
			} else {
				require.Equal(t, payload, input[0].Rule.Conditions)
				require.Equal(t, payload, input[1].Frozen.Actions)
			}
			joined := strings.Join(recorder.queries(), "\n")
			require.Contains(t, joined, "length(CAST(conditions AS BLOB))")
			require.Contains(t, joined, "CASE WHEN")
			require.NotContains(t, joined, "界")
		})
	}
}

func TestAutomationCompatibilityPreviewMatchesFreezeExactly(t *testing.T) {
	db, _, _ := compatibilityTestDB(t)
	fixtures := [][2]string{{"", "{}"}, {"{}", ""}, {`[{"key":"tier","operator":"is","value":"gold","custom_attribute_type":"contact_attribute"}]`, `[{"name":"change_status","params":"closed"}]`}, {`{"rules":[{"attribute_key":"status","filter_operator":"is","values":["open"]}]}`, `[{"capability_id":"conversation.change_priority","action_params":"urgent"}]`}, {"invalid", `[{"action_name":"assign_agent","action_params":[9007199254740993]}]`}}
	for i, fixture := range fixtures {
		rule := compatibilityTestRule(uint(i+1), 91, true)
		rule.Conditions = fixture[0]
		rule.Actions = fixture[1]
		before := rule
		preview := previewAutomationRule(rule)
		require.Equal(t, before, rule)
		require.Zero(t, preview.Version)
		require.True(t, preview.CreatedAt.IsZero())
		require.NoError(t, freezeAutomationRule(db, &rule, 4))
		var frozen domain.AutomationRuleVersion
		require.NoError(t, db.Where("rule_id = ?", rule.ID).First(&frozen).Error)
		require.Equal(t, preview.Conditions, frozen.Conditions)
		require.Equal(t, preview.Actions, frozen.Actions)
		require.Equal(t, preview.EventName, frozen.EventName)
		require.True(t, frozen.CreatedAt.Equal(rule.UpdatedAt))
		require.EqualValues(t, 4, frozen.Version)
	}
}

func TestAutomationCompatibilityResponseBudgetContinuesAndSummarizes(t *testing.T) {
	for _, individual := range []bool{false, true} {
		t.Run(fmt.Sprint(individual), func(t *testing.T) {
			db, _, _ := compatibilityTestDB(t)
			require.NoError(t, db.Create(&[]domain.AutomationRule{compatibilityTestRule(1, 91, true), compatibilityTestRule(2, 91, false), compatibilityTestRule(3, 91, true)}).Error)
			s := compatibilityTestService(db)
			s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
				out := compatibilityTestProjector(in)
				size := 1100000
				if individual {
					size = AutomationCompatibilityReportMaxBytes + 1
				}
				out.Sources[0].Issues = []domain.AutomationCompatibilityIssue{{Code: strings.Repeat("x", size), Severity: "review", Source: "saved"}}
				return out
			}
			options := compatibilityTestOptions()
			page, err := s.ListRuleCompatibility(context.Background(), 91, options)
			require.NoError(t, err)
			fits, err := automationCompatibilityFits(page)
			require.NoError(t, err)
			require.True(t, fits)
			require.NotEmpty(t, page.Rules)
			if individual {
				require.False(t, page.Complete)
				require.Equal(t, "analysis_limit_exceeded", page.Rules[0].Sources[0].Issues[0].Code)
				require.True(t, page.Rules[0].Sources[0].IssuesTruncated)
			} else {
				require.Len(t, page.Rules, 1)
				require.Equal(t, "1", page.Rules[0].ID)
				require.NotEmpty(t, page.NextCursor)
				require.EqualValues(t, 2, page.UninspectedRuleCount)
				options.Cursor = page.NextCursor
				next, err := s.ListRuleCompatibility(context.Background(), 91, options)
				require.NoError(t, err)
				require.Equal(t, "2", next.Rules[0].ID)
			}
		})
	}
}

func TestAutomationCompatibilityFailuresAreNotEmptySuccess(t *testing.T) {
	for _, table := range []string{"automation_rules", "automation_rule_versions"} {
		t.Run(table, func(t *testing.T) {
			db, _, _ := compatibilityTestDB(t)
			s := compatibilityTestService(db)
			require.NoError(t, db.Migrator().DropTable(table))
			page, err := s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
			require.ErrorIs(t, err, ErrAutomationCompatibilityRead)
			require.Empty(t, page.SchemaVersion)
		})
	}
	db, _, _ := compatibilityTestDB(t)
	s := compatibilityTestService(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.ListRuleCompatibility(ctx, 91, compatibilityTestOptions())
	require.ErrorIs(t, err, ErrAutomationCompatibilityRead)
	s.compatibilityProjector = nil
	_, err = s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.ErrorIs(t, err, ErrAutomationCompatibilityRead)
	for _, options := range []AutomationCompatibilityOptions{{}, {Target: "other"}, {Target: domain.AutomationCompatibilityTarget, RuleID: "0"}, {Target: domain.AutomationCompatibilityTarget, RuleID: "-1"}, {Target: domain.AutomationCompatibilityTarget, RuleID: "1 OR 1=1"}, {Target: domain.AutomationCompatibilityTarget, RuleID: "18446744073709551616"}, {Target: domain.AutomationCompatibilityTarget, Active: "TRUE"}, {Target: domain.AutomationCompatibilityTarget, Limit: 101}, {Target: domain.AutomationCompatibilityTarget, Limit: -1}, {Target: domain.AutomationCompatibilityTarget, Cursor: "garbage"}, {Target: domain.AutomationCompatibilityTarget, Cursor: strings.Repeat("x", 2049)}} {
		_, err := s.ListRuleCompatibility(context.Background(), 91, options)
		require.ErrorIs(t, err, ErrAutomationCompatibilityQuery)
	}
}

func TestAutomationCompatibilitySQLitePageUsesOneSnapshot(t *testing.T) {
	db, path, _ := compatibilityTestDB(t)
	require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
	rule := compatibilityTestRule(1, 91, true)
	require.NoError(t, db.Create(&rule).Error)
	require.NoError(t, db.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: 1, Version: 1, EventName: "conversation_created", Conditions: "[]", Actions: "[]"}).Error)
	writer, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	writerPool, err := writer.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = writerPool.Close() })
	var once sync.Once
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("inventory_concurrent_writer", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*automationCompatibilityRuleRow); !ok {
			return
		}
		once.Do(func() {
			require.NoError(t, writer.Transaction(func(w *gorm.DB) error {
				if err := w.Model(&domain.AutomationRule{}).Where("id = ?", 1).Update("actions", "new saved value").Error; err != nil {
					return err
				}
				return w.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: 1, Version: 2, EventName: "conversation_created", Conditions: "[]", Actions: "new frozen value"}).Error
			}))
		})
	}))
	s := compatibilityTestService(db)
	var seen domain.AutomationCompatibilityInput
	s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
		seen = in
		return compatibilityTestProjector(in)
	}
	_, err = s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.NoError(t, err)
	require.Equal(t, rule.Actions, seen.Rule.Actions)
	require.EqualValues(t, 1, seen.Frozen.Version)
	_, err = s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.NoError(t, err)
	require.Equal(t, "new saved value", seen.Rule.Actions)
	require.EqualValues(t, 2, seen.Frozen.Version)
}

func TestAutomationCompatibilitySeverityCountsAreSourceAndActiveScoped(t *testing.T) {
	db, _, _ := compatibilityTestDB(t)
	require.NoError(t, db.Create(&[]domain.AutomationRule{compatibilityTestRule(1, 91, true), compatibilityTestRule(2, 91, false)}).Error)
	s := compatibilityTestService(db)
	s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
		out := compatibilityTestProjector(in)
		for _, severity := range []string{"blocker", "review", "notice", "unknown"} {
			out.Sources[0].Issues = append(out.Sources[0].Issues, domain.AutomationCompatibilityIssue{Severity: severity})
		}
		return out
	}
	page, err := s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.NoError(t, err)
	require.Equal(t, AutomationCompatibilitySeverityCounts{2, 2, 2, 2}, page.SeverityCounts)
	require.Equal(t, AutomationCompatibilitySeverityCounts{1, 1, 1, 1}, page.ActiveSeverityCounts)
	require.Equal(t, page.ActiveSeverityCounts, page.InactiveSeverityCounts)
	raw, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "NEVER-LOAD")
}

func TestAutomationCompatibilityGuardsUnknownEventPayloads(t *testing.T) {
	db, _, recorder := compatibilityTestDB(t)
	rule := compatibilityTestRule(1, 91, true)
	rule.EventName = strings.Repeat("PRIVATE-EVENT-", 200000)
	require.NoError(t, db.Create(&rule).Error)
	require.NoError(t, db.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: 1, Version: 1, EventName: rule.EventName, Conditions: "[]", Actions: "[]"}).Error)
	s := compatibilityTestService(db)
	var seen domain.AutomationCompatibilityInput
	s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
		seen = in
		return compatibilityTestProjector(in)
	}
	recorder.reset()
	_, err := s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.NoError(t, err)
	require.Empty(t, seen.Rule.EventName)
	require.Empty(t, seen.Frozen.EventName)
	queries := strings.Join(recorder.queries(), "\n")
	require.Contains(t, queries, "CASE WHEN event_name IN")
	require.NotContains(t, queries, "PRIVATE-EVENT")
}

// Keep each final input when an unversioned safe rule used two pure passes.
func compatibilityFinalInputs(all []domain.AutomationCompatibilityInput) []domain.AutomationCompatibilityInput {
	out := make([]domain.AutomationCompatibilityInput, 0, len(all))
	for _, input := range all {
		if len(out) > 0 && out[len(out)-1].Rule.ID == input.Rule.ID {
			out[len(out)-1] = input
		} else {
			out = append(out, input)
		}
	}
	return out
}

func TestAutomationCompatibilityPreflightSkipsUnsafePreviewNormalization(t *testing.T) {
	condition := `{"attribute_key":"status","filter_operator":"is","values":["open"]}`
	fixtures := map[string]string{
		"depth":      `{"attribute_key":"status","filter_operator":"is","values":[` + strings.Repeat("[", 33) + `"open"` + strings.Repeat("]", 33) + `]}`,
		"conditions": "[" + strings.Repeat(condition+",", 1000) + condition + "]",
		"operands":   `{"attribute_key":"status","filter_operator":"is","values":[` + strings.Repeat(`"open",`, 10000) + `"open"]}`,
		"bytes":      strings.Repeat("x", domain.AutomationCompatibilityMaxBytes+1),
	}
	for name, raw := range fixtures {
		t.Run(name, func(t *testing.T) {
			db, _, _ := compatibilityTestDB(t)
			rule := compatibilityTestRule(1, 91, true)
			rule.Conditions = raw
			require.NoError(t, db.Create(&rule).Error)
			s := compatibilityTestService(db)
			calls := 0
			s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
				calls++
				require.Nil(t, in.Preview, "unsafe input never reaches normalization")
				return modruntime.AutomationRuleCompatibilityView(in)
			}
			page, err := s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.False(t, page.Complete)
			require.Len(t, page.Rules, 1)
			require.False(t, page.Rules[0].Sources[0].Complete)
			require.False(t, page.Rules[0].Sources[1].Complete)
			var codes []string
			for _, issue := range page.Rules[0].Sources[1].Issues {
				codes = append(codes, issue.Code)
			}
			require.Contains(t, codes, "activation_preview_unavailable")
		})
	}
	for _, raw := range []string{"[]", "invalid"} {
		t.Run("safe_"+raw, func(t *testing.T) {
			db, _, _ := compatibilityTestDB(t)
			rule := compatibilityTestRule(1, 91, true)
			rule.Conditions = raw
			require.NoError(t, db.Create(&rule).Error)
			s := compatibilityTestService(db)
			calls := 0
			s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
				calls++
				if calls == 1 {
					require.Nil(t, in.Preview)
				} else {
					require.NotNil(t, in.Preview)
				}
				return modruntime.AutomationRuleCompatibilityView(in)
			}
			_, err := s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
			require.NoError(t, err)
			require.Equal(t, 2, calls, "fully inspected malformed sources still use exact legacy preview")
		})
	}
}
