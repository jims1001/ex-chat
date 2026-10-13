package modruntime

import (
	"database/sql"
	"fmt"
	"math"

	"gorm.io/gorm"
)

// Explicit-ID PostgreSQL inserts do not advance SERIAL/IDENTITY sequences. A
// restored runner must be able to create new executions and owner receipts.
// ALTER SEQUENCE RESTART is transactional, unlike setval, so a failed rebuild
// rolls back sequence changes along with imported data. Restore requires an
// offline/quiesced owner database; it is not an online merge operation.
func restoreSnapshotSequences(tx *gorm.DB, models []any) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	for _, model := range models {
		statement := &gorm.Statement{DB: tx}
		if err := statement.Parse(model); err != nil {
			return err
		}
		for _, field := range statement.Schema.PrimaryFields {
			if !field.AutoIncrement {
				continue
			}
			var sequence sql.NullString
			err := tx.Raw(`SELECT quote_ident(n.nspname) || '.' || quote_ident(c.relname)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.oid = pg_get_serial_sequence(?, ?)::regclass AND c.relkind = 'S'`, statement.Schema.Table, field.DBName).Scan(&sequence).Error
			if err != nil {
				return err
			}
			if !sequence.Valid {
				continue
			}
			var maximum int64
			if err := tx.Raw("SELECT COALESCE(MAX(" + statement.Quote(field.DBName) + "), 0) FROM " + statement.Quote(statement.Schema.Table)).Scan(&maximum).Error; err != nil {
				return err
			}
			if maximum < 0 || maximum == math.MaxInt64 {
				return fmt.Errorf("restore table %s: invalid sequence maximum", statement.Schema.Table)
			}
			if err := tx.Exec(fmt.Sprintf("ALTER SEQUENCE %s RESTART WITH %d", sequence.String, maximum+1)).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
