package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	ReportingEventPageRows      = 25
	ReportingEventScanRows      = 100
	ReportingEventNameBytes     = 1020
	ReportingEventMetadataBytes = 16 * 1024
	ReportingEventResponseBytes = 1024 * 1024
)

// StoredReportingEvent is the complete persisted-record read DTO. Nullable owner
// references remain explicit nulls. Metadata is opaque text, not decoded output.
type StoredReportingEvent struct {
	ID             uint      `json:"id"`
	AccountID      uint      `json:"account_id"`
	ConversationID *uint     `json:"conversation_id"`
	UserID         *uint     `json:"user_id"`
	InboxID        *uint     `json:"inbox_id"`
	Name           string    `json:"name"`
	Value          float64   `json:"value"`
	EventStartTime time.Time `json:"event_start_time"`
	EventEndTime   time.Time `json:"event_end_time"`
	MetadataJSON   string    `json:"metadata_json"`
	CreatedAt      time.Time `json:"created_at"`
}

type StoredReportingEventPage struct {
	Events     []StoredReportingEvent `json:"events"`
	HasMore    bool                   `json:"has_more"`
	NextCursor string                 `json:"next_cursor"`
}

type ReportingEventReadRepository struct{ db *gorm.DB }

func NewReportingEventReadRepository(db *gorm.DB) *ReportingEventReadRepository {
	return &ReportingEventReadRepository{db: db}
}

// ListStored reads a single owner-local statement snapshot. Conversation
// association is tested exactly after the bounded account/name/id SQL scan, so
// sparse pages may be empty and still have a continuation. No count is implied.
func (r *ReportingEventReadRepository) ListStored(ctx context.Context, account uint, name *string, conversation, before uint) (StoredReportingEventPage, error) {
	unavailable := errors.New("stored reporting events are unavailable")
	fail := func() (StoredReportingEventPage, error) { return StoredReportingEventPage{}, unavailable }
	if r == nil || r.db == nil || r.db.Config == nil || r.db.Dialector == nil || ctx == nil || ctx.Err() != nil {
		return fail()
	}
	if account == 0 || uint64(account) > math.MaxInt64 || uint64(conversation) > math.MaxInt64 || uint64(before) > math.MaxInt64 || (name != nil && (!utf8.ValidString(*name) || len(*name) > ReportingEventNameBytes)) {
		return fail()
	}
	columns, ok := reportingEventColumns(r.db.Dialector.Name(), true)
	if !ok {
		return fail()
	}
	query := r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Model(&domain.ReportingEvent{}).Select(strings.Join(columns, ", ")).Where("account_id = ?", account)
	if name != nil {
		query = query.Where("name = ?", *name)
	}
	if before != 0 {
		query = query.Where("id < ?", before)
	}
	rows, err := query.Order("id DESC").Limit(ReportingEventScanRows + 1).Rows()
	if err != nil {
		return fail()
	}
	defer rows.Close()
	page := StoredReportingEventPage{Events: make([]StoredReportingEvent, 0, ReportingEventPageRows)}
	var last uint
	for scanned := 0; scanned < ReportingEventScanRows && len(page.Events) < ReportingEventPageRows; scanned++ {
		if !rows.Next() {
			break
		}
		var values [12]any
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if rows.Scan(dest...) != nil {
			return fail()
		}
		event, ok := storedReportingEvent(values, account, true)
		if !ok || (last != 0 && event.ID >= last) || (before != 0 && event.ID >= before) {
			return fail()
		}
		last = event.ID
		if conversation == 0 || (event.ConversationID != nil && *event.ConversationID == conversation) || reportingMetadataConversation(event.MetadataJSON, conversation) {
			page.Events = append(page.Events, event)
		}
	}
	page.HasMore = rows.Next()
	if rows.Err() != nil || rows.Close() != nil {
		return fail()
	}
	if page.HasMore {
		page.NextCursor = strconv.FormatUint(uint64(last), 10)
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded) > ReportingEventResponseBytes || ctx.Err() != nil {
		return fail()
	}
	return page, nil
}

// reportingEventColumns keeps SQLite affinity/type checks shared between the
// bounded query and complete private owner backups. Backups do not inherit query
// text, decimal or row limits.
func reportingEventColumns(dialect string, bounded bool) ([]string, bool) {
	textColumn := func(column string, limit int) string {
		switch dialect {
		case "sqlite":
			if !bounded && (column == "name" || column == "metadata_json") {
				return "CASE WHEN typeof(" + column + ") = 'text' THEN " + column + " ELSE NULL END"
			}
			return "CASE WHEN typeof(" + column + ") = 'text' AND length(CAST(" + column + " AS BLOB)) <= " + strconv.Itoa(limit) + " THEN " + column + " ELSE NULL END"
		case "postgres":
			if !bounded {
				return column + "::text"
			}
			return "CASE WHEN octet_length(" + column + "::text) <= " + strconv.Itoa(limit) + " THEN " + column + "::text ELSE NULL END"
		default:
			return ""
		}
	}
	if dialect != "sqlite" && dialect != "postgres" {
		return nil, false
	}
	columns := []string{"id", "account_id", "conversation_id", "user_id", "inbox_id", textColumn("name", ReportingEventNameBytes), "value", textColumn("event_start_time", 64), textColumn("event_end_time", 64), textColumn("metadata_json", ReportingEventMetadataBytes), textColumn("created_at", 64)}
	if dialect == "sqlite" {
		// SQLite affinity permits arbitrary blobs/text in integer and real columns.
		// Guard them before transfer; a validity bit distinguishes bad nullable IDs.
		valid := []string{}
		for i, col := range columns[:5] {
			minimum := " > 0"
			if i >= 2 {
				minimum = " >= 0"
			}
			valid = append(valid, "("+col+" IS NULL OR (typeof("+col+") = 'integer' AND "+col+minimum+"))")
		}
		columns = append(columns, "CASE WHEN "+strings.Join(valid, " AND ")+" THEN 1 ELSE 0 END")
		for i, col := range columns[:5] {
			minimum := " > 0"
			if i >= 2 {
				minimum = " >= 0"
			}
			columns[i] = "CASE WHEN typeof(" + col + ") = 'integer' AND " + col + minimum + " THEN " + col + " ELSE NULL END"
		}
		columns[6] = "CASE WHEN typeof(value) IN ('integer','real') THEN value ELSE NULL END"
	} else {
		// GORM stores float64 as PostgreSQL decimal; pgx returns decimal text.
		// Bound that text before transfer, including huge numeric/scale rows.
		columns[6] = textColumn("value", 512)
		// PostgreSQL timestamps have a fixed binary representation. Native
		// values avoid session TimeZone/DateStyle-dependent text, including
		// historical second offsets, BC formatting and infinity spellings.
		columns[7], columns[8], columns[10] = "event_start_time", "event_end_time", "created_at"
		columns = append(columns, "1")
	}
	return columns, true
}

// SnapshotRows reads complete owner records without GORM's NULL-to-zero scalar
// conversions. It is used only by quiesced private RPT snapshot creation.
func (r *ReportingEventReadRepository) SnapshotRows(ctx context.Context) ([]domain.ReportingEvent, error) {
	unavailable := errors.New("stored reporting event snapshot is unavailable")
	if r == nil || r.db == nil || r.db.Config == nil || r.db.Dialector == nil || ctx == nil || ctx.Err() != nil {
		return nil, unavailable
	}
	columns, ok := reportingEventColumns(r.db.Dialector.Name(), false)
	if !ok {
		return nil, unavailable
	}
	rows, err := r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Model(&domain.ReportingEvent{}).Select(strings.Join(columns, ", ")).Order("id ASC").Rows()
	if err != nil {
		return nil, unavailable
	}
	defer rows.Close()
	result := make([]domain.ReportingEvent, 0)
	for rows.Next() {
		var values [12]any
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if rows.Scan(dest...) != nil {
			return nil, unavailable
		}
		event, ok := storedReportingEvent(values, 0, false)
		if !ok {
			return nil, unavailable
		}
		result = append(result, domain.ReportingEvent{ID: event.ID, AccountID: event.AccountID, ConversationID: event.ConversationID, UserID: event.UserID, InboxID: event.InboxID, Name: event.Name, Value: event.Value, EventStartTime: event.EventStartTime, EventEndTime: event.EventEndTime, MetadataJSON: event.MetadataJSON, CreatedAt: event.CreatedAt})
	}
	if rows.Err() != nil || rows.Close() != nil || ctx.Err() != nil {
		return nil, unavailable
	}
	return result, nil
}

func storedReportingEvent(values [12]any, account uint, bounded bool) (StoredReportingEvent, bool) {
	var event StoredReportingEvent
	validity, ok := values[11].(int64)
	if !ok || validity != 1 {
		return event, false
	}
	id := func(raw any, nullable bool) (*uint, bool) {
		if raw == nil && nullable {
			return nil, true
		}
		n, ok := raw.(int64)
		if !ok || n < 0 || (!nullable && n == 0) || uint64(n) > uint64(^uint(0)) {
			return nil, false
		}
		v := uint(n)
		return &v, true
	}
	ids := make([]*uint, 5)
	for i := range ids {
		var ok bool
		ids[i], ok = id(values[i], i >= 2)
		if !ok {
			return event, false
		}
	}
	if account != 0 && *ids[1] != account {
		return event, false
	}
	event.ID, event.AccountID, event.ConversationID, event.UserID, event.InboxID = *ids[0], *ids[1], ids[2], ids[3], ids[4]
	var good bool
	event.Name, good = values[5].(string)
	if !good || (bounded && len(event.Name) > ReportingEventNameBytes) || !utf8.ValidString(event.Name) {
		return event, false
	}
	switch value := values[6].(type) {
	case float64:
		event.Value = value
	case int64:
		event.Value = float64(value)
	case string:
		if bounded && len(value) > 512 {
			return event, false
		}
		var err error
		event.Value, err = strconv.ParseFloat(value, 64)
		if err != nil || (event.Value == 0 && strings.ContainsAny(value, "123456789")) {
			// PostgreSQL numeric::text is plain decimal. Nonzero digits cannot
			// become zero merely because they fall below float64's range.
			return event, false
		}
	default:
		return event, false
	}
	if math.IsInf(event.Value, 0) || math.IsNaN(event.Value) {
		return event, false
	}
	event.MetadataJSON, good = values[9].(string)
	if !good || (bounded && len(event.MetadataJSON) > ReportingEventMetadataBytes) || !utf8.ValidString(event.MetadataJSON) {
		return event, false
	}
	for i, target := range map[int]*time.Time{7: &event.EventStartTime, 8: &event.EventEndTime, 10: &event.CreatedAt} {
		if value, ok := values[i].(time.Time); ok {
			*target = value.UTC()
			if _, err := target.MarshalJSON(); err != nil {
				return event, false
			}
			continue
		}
		raw, ok := values[i].(string)
		if !ok || len(raw) > 64 {
			return event, false
		}
		found := false
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999Z07", "2006-01-02 15:04:05.999999999", "2006-01-02"} {
			if value, err := time.Parse(layout, raw); err == nil {
				*target = value.UTC()
				found = true
				break
			}
		}
		if !found {
			return event, false
		}
		if _, err := target.MarshalJSON(); err != nil {
			return event, false
		}
	}
	return event, true
}

// Metadata association accepts one unambiguous top-level canonical positive
// JSON integer. It never uses float64, prefix matching, or a database JSON cast.
// Invalid/ambiguous JSON still remains a readable opaque metadata string.
func reportingMetadataConversation(metadata string, expected uint) bool {
	if !json.Valid([]byte(metadata)) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewBufferString(metadata))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool)
	match := false
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return false
		}
		seen[key] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return false
		}
		if key == "conversation_id" {
			value, err := strconv.ParseUint(string(raw), 10, 63)
			match = err == nil && value > 0 && strconv.FormatUint(value, 10) == string(raw) && value == uint64(expected)
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF && match
}
