package repository

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

var ErrCannedResponseVersioned = errors.New("canned response requires versioned OPS command")
var ErrCannedResponseDuplicate = errors.New("canned response shortcode conflicts")
var ErrCannedResponseVersion = errors.New("canned response version conflicts")

// LockCannedResponseAccount must run inside the owner transaction, before any
// receipt or owner reads. Every supported legacy/runtime writer uses this lock.
// Advisory locks are database-local; the namespaced hash avoids narrowing IDs.
func LockCannedResponseAccount(tx *gorm.DB, accountID uint) error {
	if tx == nil || accountID == 0 {
		return gorm.ErrRecordNotFound
	}
	switch tx.Dialector.Name() {
	case "postgres":
		digest := sha256.Sum256([]byte("OPS:canned_response:" + strconv.FormatUint(uint64(accountID), 10)))
		return tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(binary.BigEndian.Uint64(digest[:8]))).Error
	case "sqlite":
		// Acquire the database writer lock without changing owner data.
		return tx.Exec("UPDATE canned_responses SET id = id WHERE 1 = 0").Error
	default:
		return errors.New("unsupported canned response write backend")
	}
}

func NormalizeCannedShortcode(code string) string { return strings.ToLower(strings.TrimSpace(code)) }

// Legacy duplicates remain legal. New versioned writes conflict with every
// equivalent row; legacy writes only conflict with versioned identities.
func checkCannedShortcode(tx *gorm.DB, accountID, exceptID uint, code string, runtimeOnly bool) error {
	q := tx.Model(&domain.CannedResponse{}).Select("id, short_code").Where("account_id = ? AND id <> ?", accountID, exceptID)
	if runtimeOnly {
		q = q.Where("object_version > 0")
	}
	rows, err := q.Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	key := NormalizeCannedShortcode(code)
	for rows.Next() {
		var id uint
		var existing string
		if err := rows.Scan(&id, &existing); err != nil {
			return err
		}
		if NormalizeCannedShortcode(existing) == key {
			return ErrCannedResponseDuplicate
		}
	}
	return rows.Err()
}

// ManageVersioned is called with the receipt's transaction after its account
// lock. It does not begin or commit a transaction independently.
func (r *CannedResponseRepository) ManageVersioned(operation string, row *domain.CannedResponse, expected uint64) error {
	if row == nil || row.AccountID == 0 {
		return gorm.ErrRecordNotFound
	}
	if operation == "create" {
		if err := checkCannedShortcode(r.db, row.AccountID, 0, row.ShortCode, false); err != nil {
			return err
		}
		row.ID, row.ObjectVersion = 0, 1
		return r.db.Create(row).Error
	}
	var stored domain.CannedResponse
	if err := r.db.Where("account_id = ? AND id = ?", row.AccountID, row.ID).First(&stored).Error; err != nil {
		return err
	}
	if stored.ObjectVersion <= 0 || expected == 0 || expected >= math.MaxInt64 || uint64(stored.ObjectVersion) != expected {
		return ErrCannedResponseVersion
	}
	row.ObjectVersion = stored.ObjectVersion + 1
	row.CreatedAt = stored.CreatedAt
	switch operation {
	case "update":
		if err := checkCannedShortcode(r.db, row.AccountID, row.ID, row.ShortCode, false); err != nil {
			return err
		}
		row.UpdatedAt = r.db.NowFunc()
		result := r.db.Model(&domain.CannedResponse{}).Where("account_id = ? AND id = ? AND object_version = ?", row.AccountID, row.ID, expected).Updates(map[string]any{"short_code": row.ShortCode, "content": row.Content, "object_version": row.ObjectVersion, "updated_at": row.UpdatedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCannedResponseVersion
		}
		return nil
	case "delete":
		result := r.db.Where("account_id = ? AND id = ? AND object_version = ?", row.AccountID, row.ID, expected).Delete(&domain.CannedResponse{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCannedResponseVersion
		}
		return nil
	default:
		return errors.New("unsupported canned response operation")
	}
}
