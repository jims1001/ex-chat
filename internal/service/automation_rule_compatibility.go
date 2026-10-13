package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

const AutomationCompatibilityReportMaxBytes = 2 << 20

// Event names are an exact allowlist. SQLite does not enforce model varchar
// lengths, so an unknown event must not materialize an unbounded raw value.
const automationCompatibilityEventProjection = "CASE WHEN event_name IN ('conversation_created', 'conversation_updated', 'conversation_opened', 'conversation_resolved', 'message_created') THEN event_name ELSE '' END AS event_name"

// Only an explicitly adopted known profile reaches the pure inspector. Unknown
// persisted text is reduced in SQL so even malformed SQLite rows cannot cause an
// unbounded profile allocation or reveal arbitrary saved text in the report.
const automationCompatibilitySemanticsProjection = "CASE WHEN COALESCE(condition_semantics, '') = '' THEN '' WHEN condition_semantics = '" + domain.AutomationNumericCustomV1 + "' THEN '" + domain.AutomationNumericCustomV1 + "' ELSE 'unknown' END AS condition_semantics"

var ErrAutomationCompatibilityQuery = errors.New("invalid automation compatibility query")
var ErrAutomationCompatibilityRead = errors.New("automation compatibility inventory unavailable")

type AutomationCompatibilityOptions struct {
	Target string
	RuleID string
	Active string
	Limit  int
	Cursor string
}
type AutomationCompatibilityFilters struct {
	RuleID string `json:"rule_id,omitempty"`
	Active string `json:"active"`
}
type AutomationCompatibilitySeverityCounts struct {
	Blocker int `json:"blocker"`
	Review  int `json:"review"`
	Notice  int `json:"notice"`
	Unknown int `json:"unknown"`
}
type AutomationCompatibilityActiveCounts struct {
	Active   int `json:"active"`
	Inactive int `json:"inactive"`
}
type AutomationCompatibilityPage struct {
	SchemaVersion          string                         `json:"schema_version"`
	Target                 string                         `json:"target"`
	ImplementationRevision string                         `json:"implementation_revision"`
	CompilerPolicy         string                         `json:"compiler_policy"`
	ConditionPolicy        string                         `json:"condition_policy"`
	ActivityPolicy         string                         `json:"activity_policy"`
	SeverityCountScope     string                         `json:"severity_count_scope"`
	SourceKind             string                         `json:"source_kind"`
	AccountID              string                         `json:"account_id"`
	ObservedAt             time.Time                      `json:"observed_at"`
	Filters                AutomationCompatibilityFilters `json:"filters"`
	Horizon                string                         `json:"horizon"`
	Consistency            string                         `json:"consistency"`
	CollectionSnapshot     bool                           `json:"collection_snapshot"`
	LiveCollectionCaveat   string                         `json:"live_collection_caveat"`
	PageExhausted          bool                           `json:"page_exhausted"`
	Complete               bool                           `json:"complete"`
	InspectedRuleCount     int                            `json:"inspected_rule_count"`
	// Remaining matching rows after this page, within the horizon and page snapshot.
	// This is not a count from an atomic multi-page collection snapshot.
	UninspectedRuleCount   int64                                 `json:"uninspected_rule_count"`
	SeverityCounts         AutomationCompatibilitySeverityCounts `json:"severity_counts"`
	ActiveSeverityCounts   AutomationCompatibilitySeverityCounts `json:"active_severity_counts"`
	InactiveSeverityCounts AutomationCompatibilitySeverityCounts `json:"inactive_severity_counts"`
	ActiveCounts           AutomationCompatibilityActiveCounts   `json:"active_counts"`
	NotChecked             []string                              `json:"not_checked"`
	Rules                  []domain.AutomationCompatibilityRule  `json:"rules"`
	NextCursor             string                                `json:"next_cursor,omitempty"`
}
type automationCompatibilityCursor struct {
	Schema    string `json:"schema"`
	Target    string `json:"target"`
	Revision  string `json:"revision"`
	AccountID uint   `json:"account"`
	RuleID    string `json:"rule"`
	Active    string `json:"active"`
	Horizon   uint   `json:"horizon"`
	LastID    uint   `json:"last_id"`
}

// Configure during composition, independently of legacy/durable execution mode.
// This is a pure projection, not a bridge, owner lookup, or activation operation.
func (s *AutomationService) SetAutomationCompatibilityProjector(projector func(domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule) {
	s.compatibilityProjector = projector
}

func automationCompatibilityPosition(accountID uint, options *AutomationCompatibilityOptions) (automationCompatibilityCursor, error) {
	bad := automationCompatibilityCursor{}
	if accountID == 0 || uint64(accountID) > math.MaxInt64 || options.Target != domain.AutomationCompatibilityTarget {
		return bad, ErrAutomationCompatibilityQuery
	}
	if options.Active == "" {
		options.Active = "all"
	}
	if options.Active != "all" && options.Active != "true" && options.Active != "false" {
		return bad, ErrAutomationCompatibilityQuery
	}
	if options.Limit == 0 {
		options.Limit = 100
	}
	if options.Limit < 1 || options.Limit > 100 || len(options.Cursor) > 2048 {
		return bad, ErrAutomationCompatibilityQuery
	}
	var err error
	options.RuleID, err = automationHistoryID(options.RuleID)
	if err != nil {
		return bad, ErrAutomationCompatibilityQuery
	}
	position := automationCompatibilityCursor{Schema: domain.AutomationCompatibilitySchema, Target: options.Target, Revision: domain.AutomationCompatibilityRevision, AccountID: accountID, RuleID: options.RuleID, Active: options.Active}
	if options.Cursor == "" {
		return position, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(options.Cursor)
	if err != nil {
		return bad, ErrAutomationCompatibilityQuery
	}
	var cursor automationCompatibilityCursor
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF {
		return bad, ErrAutomationCompatibilityQuery
	}
	// Only our canonical cursor encoding is accepted. This also rejects duplicate
	// JSON members, which encoding/json would otherwise silently resolve last-wins.
	canonical, _ := json.Marshal(cursor)
	if !bytes.Equal(raw, canonical) || cursor.Schema != position.Schema || cursor.Target != position.Target || cursor.Revision != position.Revision || cursor.AccountID != position.AccountID || cursor.RuleID != position.RuleID || cursor.Active != position.Active || cursor.LastID == 0 || cursor.Horizon < cursor.LastID || uint64(cursor.Horizon) > math.MaxInt64 {
		return bad, ErrAutomationCompatibilityQuery
	}
	return cursor, nil
}

func automationCompatibilityQuery(tx *gorm.DB, accountID uint, options AutomationCompatibilityOptions, position automationCompatibilityCursor) *gorm.DB {
	q := tx.Model(&domain.AutomationRule{}).Where("account_id = ? AND id <= ? AND id > ?", accountID, position.Horizon, position.LastID)
	if options.RuleID != "" {
		q = q.Where("id = ?", options.RuleID)
	}
	if options.Active != "all" {
		q = q.Where("active = ?", options.Active == "true")
	}
	return q
}

// The guarded projection checks encoded BYTES in SQL, not characters and not Go
// lengths after loading. Payloads are loaded for one rule at a time; no name or
// description is read. Both supported dialects return empty text for oversized
// blobs and carry the original byte lengths into the pure inspector.
func automationCompatibilityProjection(dialect string) (string, error) {
	length := func(column string) string { return "COALESCE(length(CAST(" + column + " AS BLOB)), 0)" }
	if dialect == "postgres" {
		length = func(column string) string { return "COALESCE(octet_length(" + column + "), 0)" }
	} else if dialect != "sqlite" {
		return "", ErrAutomationCompatibilityRead
	}
	conditions, actions := length("conditions"), length("actions")
	max := strconv.Itoa(domain.AutomationCompatibilityMaxBytes)
	return "CASE WHEN " + conditions + " <= " + max + " THEN COALESCE(conditions, '') ELSE '' END AS conditions, CASE WHEN " + actions + " <= " + max + " THEN COALESCE(actions, '') ELSE '' END AS actions, " + conditions + " AS condition_bytes, " + actions + " AS action_bytes", nil
}

type automationCompatibilityRuleRow struct {
	domain.AutomationRule `gorm:"embedded"`
	ConditionBytes        int
	ActionBytes           int
}
type automationCompatibilityVersionRow struct {
	domain.AutomationRuleVersion `gorm:"embedded"`
	ConditionBytes               int
	ActionBytes                  int
}

func (s *AutomationService) ListRuleCompatibility(ctx context.Context, accountID uint, options AutomationCompatibilityOptions) (AutomationCompatibilityPage, error) {
	position, err := automationCompatibilityPosition(accountID, &options)
	if err != nil {
		return AutomationCompatibilityPage{}, err
	}
	if s.db == nil || s.compatibilityProjector == nil {
		return AutomationCompatibilityPage{}, ErrAutomationCompatibilityRead
	}
	projection, err := automationCompatibilityProjection(s.db.Dialector.Name())
	if err != nil {
		return AutomationCompatibilityPage{}, err
	}
	page := AutomationCompatibilityPage{
		SchemaVersion: domain.AutomationCompatibilitySchema, Target: options.Target, ImplementationRevision: domain.AutomationCompatibilityRevision,
		CompilerPolicy: "continue_definite.v1", ConditionPolicy: "per_rule_version.v1", ActivityPolicy: "touch_valid.v1", SeverityCountScope: "returned_issues", SourceKind: "saved_account_page", AccountID: strconv.FormatUint(uint64(accountID), 10),
		Filters: AutomationCompatibilityFilters{RuleID: options.RuleID, Active: options.Active}, Consistency: "page_snapshot_live_collection", CollectionSnapshot: false,
		LiveCollectionCaveat: "A fixed rule-ID horizon excludes later higher IDs; edits, active-state changes, deletes, and new versions remain live between pages. Re-run against a quiesced or exported source before a separately approved cutover.",
		Complete:             true, PageExhausted: true, NotChecked: []string{"target_existence", "owner_reachability", "credentials_permissions", "provider_channel_suitability", "client_adoption", "actual_migration_rollout", "custom_attribute_catalog", "custom_attribute_type", "custom_attribute_value", "configuration_presence"}, Rules: make([]domain.AutomationCompatibilityRule, 0),
	}
	// PostgreSQL READ COMMITTED can observe different rule/version generations
	// inside one transaction; explicitly require a repeatable read-only snapshot.
	// SQLite's read transaction fixes its snapshot at the first SELECT.
	txOptions := &sql.TxOptions{ReadOnly: true}
	if s.db.Dialector.Name() == "postgres" {
		txOptions.Isolation = sql.LevelRepeatableRead
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		page.ObservedAt = time.Now().UTC()
		if options.Cursor == "" {
			if err := tx.Model(&domain.AutomationRule{}).Where("account_id = ?", accountID).Select("COALESCE(MAX(id), 0)").Scan(&position.Horizon).Error; err != nil {
				return err
			}
		}
		page.Horizon = strconv.FormatUint(uint64(position.Horizon), 10)
		var remaining int64
		if err := automationCompatibilityQuery(tx, accountID, options, position).Count(&remaining).Error; err != nil {
			return err
		}
		// Probe the version table even for empty inventories; an unavailable AUT
		// source must never masquerade as a successful empty account.
		var versionProbe []uint
		if err := tx.Model(&domain.AutomationRuleVersion{}).Where("account_id = ?", accountID).Select("id").Limit(1).Find(&versionProbe).Error; err != nil {
			return err
		}
		var ids []uint
		if err := automationCompatibilityQuery(tx, accountID, options, position).Order("id ASC").Limit(options.Limit+1).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > options.Limit {
			ids = ids[:options.Limit]
		}
		for _, id := range ids {
			var row automationCompatibilityRuleRow
			if err := tx.Model(&domain.AutomationRule{}).Select("id, account_id, "+automationCompatibilityEventProjection+", "+automationCompatibilitySemanticsProjection+", active, updated_at, "+projection).Where("account_id = ? AND id = ?", accountID, id).Take(&row).Error; err != nil {
				return err
			}
			input := domain.AutomationCompatibilityInput{Rule: row.AutomationRule, SavedConditionBytes: row.ConditionBytes, SavedActionBytes: row.ActionBytes}
			var frozen automationCompatibilityVersionRow
			err := tx.Model(&domain.AutomationRuleVersion{}).Select("id, account_id, rule_id, version, "+automationCompatibilityEventProjection+", "+automationCompatibilitySemanticsProjection+", created_at, "+projection).Where("account_id = ? AND rule_id = ?", accountID, id).Order("version DESC, id DESC").Take(&frozen).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var view domain.AutomationCompatibilityRule
			if err == nil {
				input.Frozen = &frozen.AutomationRuleVersion
				input.EffectiveConditionBytes = frozen.ConditionBytes
				input.EffectiveActionBytes = frozen.ActionBytes
				view = s.compatibilityProjector(input)
			} else {
				// First inspect the saved source without normalizing it. A source
				// outside depth/count/operand/issue bounds must not reach the legacy
				// freeze normalizer. Safe unversioned rules use two pure projections;
				// neither one can touch owner clients or create a frozen version.
				view = s.compatibilityProjector(input)
				savedComplete := false
				for _, source := range view.Sources {
					if source.Source == "saved" {
						savedComplete = source.Complete
						break
					}
				}
				if savedComplete && row.ConditionBytes <= domain.AutomationCompatibilityMaxBytes && row.ActionBytes <= domain.AutomationCompatibilityMaxBytes {
					preview := previewAutomationRule(row.AutomationRule)
					input.EffectiveConditionBytes = len(preview.Conditions)
					input.EffectiveActionBytes = len(preview.Actions)
					input.Preview = &preview
					view = s.compatibilityProjector(input)
				}
			}
			candidate := automationCompatibilityAppend(page, view, position, id, remaining)
			fits, err := automationCompatibilityFits(candidate)
			if err != nil {
				return err
			}
			if !fits {
				if len(page.Rules) > 0 {
					break
				}
				// Guarantee forward progress even if one projected rule alone exceeds the
				// response bound. It remains explicitly unassessed, never compatible.
				candidate = automationCompatibilityAppend(page, automationCompatibilitySummary(view), position, id, remaining)
				if fits, err = automationCompatibilityFits(candidate); err != nil || !fits {
					return ErrAutomationCompatibilityRead
				}
			}
			page = candidate
		}
		return nil
	}, txOptions)
	if err != nil {
		return AutomationCompatibilityPage{}, ErrAutomationCompatibilityRead
	}
	return page, nil
}

func (counts *AutomationCompatibilitySeverityCounts) add(severity string) {
	switch severity {
	case "blocker":
		counts.Blocker++
	case "review":
		counts.Review++
	case "notice":
		counts.Notice++
	case "unknown":
		counts.Unknown++
	}
}
func automationCompatibilityAppend(page AutomationCompatibilityPage, rule domain.AutomationCompatibilityRule, position automationCompatibilityCursor, id uint, remaining int64) AutomationCompatibilityPage {
	page.Rules = append(page.Rules, rule)
	page.InspectedRuleCount = len(page.Rules)
	page.UninspectedRuleCount = remaining - int64(len(page.Rules))
	page.PageExhausted = page.UninspectedRuleCount == 0
	page.Complete = page.Complete && rule.Complete
	if rule.Active {
		page.ActiveCounts.Active++
	} else {
		page.ActiveCounts.Inactive++
	}
	for _, source := range rule.Sources {
		for _, issue := range source.Issues {
			page.SeverityCounts.add(issue.Severity)
			if rule.Active {
				page.ActiveSeverityCounts.add(issue.Severity)
			} else {
				page.InactiveSeverityCounts.add(issue.Severity)
			}
		}
	}
	if !page.PageExhausted {
		position.LastID = id
		raw, _ := json.Marshal(position)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	} else {
		page.NextCursor = ""
	}
	return page
}
func automationCompatibilityFits(page AutomationCompatibilityPage) (bool, error) {
	// Include the actual HTTP success-envelope overhead in the 2 MiB budget.
	raw, err := json.Marshal(struct {
		Success bool                        `json:"success"`
		Data    AutomationCompatibilityPage `json:"data"`
	}{true, page})
	return len(raw) <= AutomationCompatibilityReportMaxBytes, err
}
func automationCompatibilitySummary(rule domain.AutomationCompatibilityRule) domain.AutomationCompatibilityRule {
	rule.Complete = false
	// Restrict this fallback to stable metadata plus one bounded issue per source.
	sources := make([]domain.AutomationCompatibilitySource, 0, 2)
	for _, source := range rule.Sources {
		if source.Source != "saved" && source.Source != "effective_frozen" && source.Source != "activation_preview" {
			continue
		}
		sources = append(sources, domain.AutomationCompatibilitySource{Source: source.Source, Event: source.Event, ConditionSemantics: source.ConditionSemantics, Complete: false, AssessmentLevel: "not_assessed", IssuesTruncated: true, FailedLogicalIndexes: []int{}, RequiredOwners: []string{}, Issues: []domain.AutomationCompatibilityIssue{{Code: "analysis_limit_exceeded", Category: "limit", Severity: "unknown", Source: source.Source, Path: "/", RuntimeEffect: "not_assessed", Details: domain.AutomationCompatibilityDetails{Limit: AutomationCompatibilityReportMaxBytes, Unit: "bytes"}}}})
		if len(sources) == 2 {
			break
		}
	}
	rule.Sources = sources
	return rule
}
