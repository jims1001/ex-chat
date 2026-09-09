package pagination

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	DefaultPage     = 1
	DefaultPageSize = 25
	MaxPageSize     = 100
)

// Params encapsulates pagination input parameters
type Params struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// NewParams creates validated Params
func NewParams(page, pageSize int) Params {
	if page < 1 {
		page = DefaultPage
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	} else if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return Params{
		Page:     page,
		PageSize: pageSize,
	}
}

// Offset returns the SQL query offset
func (p Params) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// Limit returns the SQL query limit
func (p Params) Limit() int {
	return p.PageSize
}

// Meta describes the pagination outcome
type Meta struct {
	Total      int64 `json:"total"`
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalPages int   `json:"total_pages"`
	HasNext    bool  `json:"has_next"`
	HasPrev    bool  `json:"has_prev"`
}

// NewMeta calculates pagination metadata
func NewMeta(total int64, page, pageSize int) Meta {
	if page < 1 {
		page = DefaultPage
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}

	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}

	return Meta{
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
		HasNext:    page < totalPages,
		HasPrev:    page > 1 && totalPages > 0,
	}
}

// Result encapsulates paginated list items with pagination metadata
type Result struct {
	Items      any  `json:"items"`
	Pagination Meta `json:"pagination"`
}

// NewResult constructs a Result
func NewResult(items any, total int64, page, pageSize int) Result {
	return Result{
		Items:      items,
		Pagination: NewMeta(total, page, pageSize),
	}
}

// EncodeCursor encodes ID and Unix timestamp into a base64 cursor token
func EncodeCursor(id uint, timestamp int64) string {
	raw := fmt.Sprintf("%d:%d", id, timestamp)
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor decodes ID and Unix timestamp from a cursor token
func DecodeCursor(cursor string) (uint, int64, error) {
	rawBytes, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cursor encoding: %w", err)
	}

	parts := strings.Split(string(rawBytes), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid cursor format")
	}

	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cursor id: %w", err)
	}

	ts, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cursor timestamp: %w", err)
	}

	return uint(id), ts, nil
}
