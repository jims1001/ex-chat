package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

const CannedResponseReadBytes = 1 << 20
const CannedResponseReadLimit = 100

// StoredCannedResponse freezes the six-field read projection independently of
// future mutable model fields.
type StoredCannedResponse struct {
	ID        uint      `json:"id"`
	AccountID uint      `json:"account_id"`
	ShortCode string    `json:"short_code"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CannedResponseReadPage exposes only account-shared, persisted records.
// The cursor is the last returned ID when a lower matching ID exists.
type CannedResponseReadPage struct {
	Items      []StoredCannedResponse `json:"items"`
	NextCursor string                 `json:"next_cursor"`
	// Only populated internally for the explicit version-aware detail projection.
	DetailVersion int64 `json:"-"`
}

var ErrCannedResponseReadBounds = errors.New("canned response exceeds read bounds")

// ReadBounded preserves the existing List and FindMatch predicates and ordering.
// Text is byte-bounded in SQL before transfer, then rows are scanned under a
// cumulative encoded-data budget. It never changes stored legacy records.
func (r *CannedResponseRepository) ReadBounded(ctx context.Context, accountID uint, mode string, id uint, text string, before uint, limit int) (*CannedResponseReadPage, error) {
	if r == nil || r.db == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrCannedResponseReadBounds
	}
	if accountID == 0 || limit < 1 || limit > CannedResponseReadLimit {
		return nil, ErrCannedResponseReadBounds
	}
	query := r.db.WithContext(ctx).Model(&domain.CannedResponse{}).Where("account_id = ?", accountID)
	switch mode {
	case "detail", "detail_versioned":
		if id == 0 {
			return nil, gorm.ErrRecordNotFound
		}
		query = query.Where("id = ?", id).Order("id ASC").Limit(1)
	case "match":
		text = strings.ToLower(strings.TrimSpace(text))
		query = query.Where("LOWER(short_code) = ? OR LOWER(content) LIKE ?", text, "%"+text+"%").Order("id ASC").Limit(1)
	case "list":
		if text != "" {
			value := "%" + strings.ToLower(text) + "%"
			query = query.Where("LOWER(short_code) LIKE ? OR LOWER(content) LIKE ?", value, value)
		}
		if before != 0 {
			query = query.Where("id < ?", before)
		}
		query = query.Order("id DESC").Limit(limit + 1)
	default:
		return nil, ErrCannedResponseReadBounds
	}
	length := func(field string) string { return "octet_length(" + field + ")" }
	switch r.db.Dialector.Name() {
	case "sqlite":
		length = func(field string) string { return "length(CAST(" + field + " AS BLOB))" }
	case "postgres":
	default:
		return nil, ErrCannedResponseReadBounds
	}
	guard := length("short_code") + " <= ? AND " + length("content") + " <= ?"
	projection := "id, account_id, CASE WHEN " + guard + " THEN short_code ELSE '' END AS short_code, CASE WHEN " + guard + " THEN content ELSE '' END AS content, created_at, updated_at, CASE WHEN " + guard + " THEN 0 ELSE 1 END AS oversized"
	if mode == "detail_versioned" {
		projection += ", object_version"
	}
	rows, err := query.Select(projection, CannedResponseReadBytes, CannedResponseReadBytes, CannedResponseReadBytes, CannedResponseReadBytes, CannedResponseReadBytes, CannedResponseReadBytes).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &CannedResponseReadPage{Items: []StoredCannedResponse{}}
	// Fixed page keys/array delimiters, commas and a maximum cursor fit in 128 bytes.
	size := 128
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if mode == "list" && len(page.Items) == limit {
			page.NextCursor = strconv.FormatUint(uint64(page.Items[len(page.Items)-1].ID), 10)
			break
		}
		var row struct {
			StoredCannedResponse
			Oversized     int
			ObjectVersion int64
		}
		if err := r.db.ScanRows(rows, &row); err != nil {
			return nil, err
		}
		if row.Oversized != 0 || row.AccountID != accountID || row.ID == 0 || !utf8.ValidString(row.ShortCode) || !utf8.ValidString(row.Content) {
			return nil, ErrCannedResponseReadBounds
		}
		encoded, err := json.Marshal(row.StoredCannedResponse)
		if err != nil {
			return nil, err
		}
		if mode == "detail_versioned" {
			if row.ObjectVersion < 0 {
				return nil, ErrCannedResponseReadBounds
			}
			page.DetailVersion = row.ObjectVersion
			// Include the additional key and full int64 representation in the budget.
			size += len(`,"object_version":`) + len(strconv.FormatInt(row.ObjectVersion, 10))
		}
		size += len(encoded) + 1
		if size > CannedResponseReadBytes {
			return nil, ErrCannedResponseReadBounds
		}
		page.Items = append(page.Items, row.StoredCannedResponse)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if mode != "list" && len(page.Items) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return page, nil
}
