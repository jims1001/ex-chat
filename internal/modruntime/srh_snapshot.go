package modruntime

import (
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// A deleted filter ID remains an identity in durable receipts and events. Restore
// must retain that high-water mark, or a stale CAS target could name a new row.
// Like the rest of snapshot restore, this requires a quiesced private owner store.
func restoreSavedFilterSequence(tx *gorm.DB) error {
	var existing, historical int64
	if err := tx.Model(&domain.CustomFilter{}).Select("COALESCE(MAX(id), 0)").Scan(&existing).Error; err != nil {
		return err
	}
	if err := tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND object_type = ? AND action IN ?", "SRH", "CustomFilter", []string{"create", "update", "delete"}).Select("COALESCE(MAX(object_id), 0)").Scan(&historical).Error; err != nil {
		return err
	}
	maximum := max(existing, historical)
	if maximum < 0 || maximum == math.MaxInt64 {
		return errors.New("saved filter snapshot sequence is invalid")
	}
	switch tx.Dialector.Name() {
	case "sqlite":
		result := tx.Exec("UPDATE sqlite_sequence SET seq = MAX(seq, ?) WHERE name = ?", maximum, "custom_filters")
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return tx.Exec("INSERT INTO sqlite_sequence(name, seq) VALUES (?, ?)", "custom_filters", maximum).Error
		}
		return nil
	case "postgres":
		var sequence sql.NullString
		if err := tx.Raw(`SELECT quote_ident(n.nspname) || '.' || quote_ident(c.relname)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.oid = pg_get_serial_sequence('custom_filters', 'id')::regclass AND c.relkind = 'S'`).Scan(&sequence).Error; err != nil {
			return err
		}
		if !sequence.Valid {
			return errors.New("saved filter snapshot sequence is unavailable")
		}
		return tx.Exec(fmt.Sprintf("ALTER SEQUENCE %s RESTART WITH %d", sequence.String, maximum+1)).Error
	default:
		return errors.New("saved filter snapshot sequence backend is unsupported")
	}
}
