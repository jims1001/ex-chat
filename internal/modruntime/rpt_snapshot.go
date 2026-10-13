package modruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// restoreRPTReportingRows preserves explicit zero Value and CreatedAt, which
// struct Create otherwise replaces with a default and the current time. Existing
// snapshots omit nil relationship IDs; absent or explicit null remains nil.
// Only this RPT model uses the hook, inside the existing restore transaction.
func restoreRPTReportingRows(tx *gorm.DB, model any, raw []byte) (bool, error) {
	if _, ok := model.(*domain.ReportingEvent); !ok {
		return false, nil
	}
	invalid := errors.New("invalid stored reporting events")
	if !utf8.Valid(raw) {
		return true, invalid
	}
	var encoded []json.RawMessage
	if json.Unmarshal(raw, &encoded) != nil || encoded == nil {
		return true, invalid
	}
	for _, data := range encoded {
		fields, err := rptSnapshotFields(data)
		if err != nil {
			return true, invalid
		}
		var row domain.ReportingEvent
		if json.Unmarshal(data, &row) != nil || row.ID == 0 || row.AccountID == 0 || math.IsNaN(row.Value) || math.IsInf(row.Value, 0) {
			return true, invalid
		}
		mantissa := strings.SplitN(strings.ToLower(string(fields["value"])), "e", 2)[0]
		if row.Value == 0 && strings.ContainsAny(mantissa, "123456789") {
			return true, invalid
		}
		if tx.Dialector.Name() == "postgres" {
			for _, stamp := range []time.Time{row.EventStartTime, row.EventEndTime, row.CreatedAt} {
				if stamp.Nanosecond()%1000 != 0 {
					return true, invalid
				}
			}
		}
		for key, value := range fields {
			switch key {
			case "conversation_id", "user_id", "inbox_id":
				if bytes.Equal(value, []byte("null")) {
					continue
				}
				fallthrough
			case "id", "account_id":
				n, err := strconv.ParseUint(string(value), 10, strconv.IntSize)
				if err != nil || strconv.FormatUint(n, 10) != string(value) {
					return true, invalid
				}
			default:
				if bytes.Equal(value, []byte("null")) {
					return true, invalid
				}
			}
		}
		if _, err := row.EventStartTime.MarshalJSON(); err != nil {
			return true, invalid
		}
		if _, err := row.EventEndTime.MarshalJSON(); err != nil {
			return true, invalid
		}
		if _, err := row.CreatedAt.MarshalJSON(); err != nil {
			return true, invalid
		}
		err = tx.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Model(&domain.ReportingEvent{}).Create(map[string]any{
			"id": row.ID, "account_id": row.AccountID, "conversation_id": row.ConversationID, "user_id": row.UserID, "inbox_id": row.InboxID, "name": row.Name, "value": row.Value, "event_start_time": row.EventStartTime, "event_end_time": row.EventEndTime, "metadata_json": row.MetadataJSON, "created_at": row.CreatedAt,
		}).Error
		if err != nil {
			return true, invalid
		}
	}
	return true, nil
}

func rptSnapshotFields(data []byte) (map[string]json.RawMessage, error) {
	invalid := errors.New("invalid stored reporting event fields")
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, invalid
		}
		key, ok := token.(string)
		if !ok {
			return nil, invalid
		}
		known := false
		for _, allowed := range storedReportingEventsFields {
			if key == allowed {
				known = true
				break
			}
		}
		if !known || fields[key] != nil {
			return nil, invalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, invalid
		}
		fields[key] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, invalid
	}
	for _, key := range []string{"id", "account_id", "name", "value", "event_start_time", "event_end_time", "metadata_json", "created_at"} {
		if fields[key] == nil {
			return nil, invalid
		}
	}
	return fields, nil
}

func createRPTReportingSnapshot(db *gorm.DB, model any) ([]byte, bool, error) {
	if _, ok := model.(*domain.ReportingEvent); !ok {
		return nil, false, nil
	}
	rows, err := repository.NewReportingEventReadRepository(db).SnapshotRows(db.Statement.Context)
	if err != nil {
		return nil, true, errors.New("stored reporting event snapshot is unavailable")
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return nil, true, errors.New("stored reporting event snapshot is unavailable")
	}
	return encoded, true, nil
}
