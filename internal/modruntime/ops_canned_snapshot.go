package modruntime

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

const cannedSnapshotCodec = "ops.canned_response.v1"

type cannedSnapshotRow struct {
	domain.CannedResponse
	Version *int64 `json:"object_version"`
}
type cannedSnapshot struct {
	Codec string              `json:"codec"`
	Rows  []cannedSnapshotRow `json:"rows"`
}

func marshalCannedSnapshot(rows []domain.CannedResponse) ([]byte, error) {
	out := cannedSnapshot{Codec: cannedSnapshotCodec, Rows: make([]cannedSnapshotRow, len(rows))}
	for i, row := range rows {
		version := row.ObjectVersion
		out.Rows[i] = cannedSnapshotRow{CannedResponse: row, Version: &version}
	}
	return json.Marshal(out)
}
func unmarshalCannedSnapshot(raw []byte, target *[]domain.CannedResponse) error {
	invalid := errors.New("canned response snapshot is invalid")
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return invalid
	}
	if raw[0] == '[' {
		// Explicit compatibility branch for historical six-field snapshots only.
		var fields []map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return invalid
		}
		for _, row := range fields {
			if len(row) != 6 {
				return invalid
			}
			for _, key := range cannedResponseFields {
				if _, ok := row[key]; !ok {
					return invalid
				}
			}
		}
		if json.Unmarshal(raw, target) != nil {
			return invalid
		}
		return nil
	}
	var snapshot cannedSnapshot
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || snapshot.Codec != cannedSnapshotCodec || snapshot.Rows == nil {
		return invalid
	}
	rows := make([]domain.CannedResponse, len(snapshot.Rows))
	keys := map[string]bool{}
	for i, item := range snapshot.Rows {
		if item.Version == nil || *item.Version < 0 || item.ID == 0 || item.AccountID == 0 {
			return invalid
		}
		row := item.CannedResponse
		row.ObjectVersion = *item.Version
		if row.ObjectVersion > 0 && (row.ShortCode == "" || repository.NormalizeCannedShortcode(row.ShortCode) != row.ShortCode || !utf8.ValidString(row.ShortCode) || utf8.RuneCountInString(row.ShortCode) > 100 || row.Content == "" || !utf8.ValidString(row.Content)) {
			return invalid
		}
		key := strconv.FormatUint(uint64(row.AccountID), 10) + ":" + repository.NormalizeCannedShortcode(row.ShortCode)
		if prior, exists := keys[key]; exists && (prior || row.ObjectVersion > 0) {
			return invalid
		}
		keys[key] = row.ObjectVersion > 0
		rows[i] = row
	}
	*target = rows
	return nil
}

// Deleted versioned IDs survive in durable changed outbox records. Restoring an
// empty table must not reuse an identity still named by receipts and old CAS.
func restoreCannedSequence(tx *gorm.DB) error {
	var maximum int64
	if err := tx.Model(&domain.CannedResponse{}).Select("COALESCE(MAX(id),0)").Scan(&maximum).Error; err != nil {
		return err
	}
	var historical []string
	if err := tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND object_type = ? AND action = ?", "OPS", "CannedResponse", "event_outbox").Pluck("object_display_id", &historical).Error; err != nil {
		return err
	}
	for _, raw := range historical {
		id, ok := cannedResponseID(raw)
		if !ok {
			return errors.New("canned response historical identity is invalid")
		}
		if int64(id) > maximum {
			maximum = int64(id)
		}
	}
	if maximum < 0 || maximum == math.MaxInt64 {
		return errors.New("canned response sequence is exhausted")
	}
	switch tx.Dialector.Name() {
	case "sqlite":
		result := tx.Exec("UPDATE sqlite_sequence SET seq = MAX(seq, ?) WHERE name = ?", maximum, "canned_responses")
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return tx.Exec("INSERT INTO sqlite_sequence(name,seq) VALUES (?,?)", "canned_responses", maximum).Error
		}
		return nil
	case "postgres":
		var sequence sql.NullString
		if err := tx.Raw(`SELECT quote_ident(n.nspname) || '.' || quote_ident(c.relname)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.oid = pg_get_serial_sequence('canned_responses','id')::regclass AND c.relkind='S'`).Scan(&sequence).Error; err != nil {
			return err
		}
		if !sequence.Valid || strings.TrimSpace(sequence.String) == "" {
			return errors.New("canned response sequence is unavailable")
		}
		return tx.Exec(fmt.Sprintf("ALTER SEQUENCE %s RESTART WITH %d", sequence.String, maximum+1)).Error
	default:
		return errors.New("unsupported canned response snapshot backend")
	}
}
