package repository

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// automationFactScan is a pass-local, owner-bound snapshot cursor. It contains
// no mutable repository state and never advances the owner's durable receipts.
// New facts created while AUT drains one page belong to the next scan, bounding
// this pass even when its processing itself emits more owner facts.
type automationFactScan struct {
	Version int    `json:"version"`
	Owner   string `json:"owner"`
	Horizon uint   `json:"horizon"`
	AfterID uint   `json:"after_id"`
	Stage   int    `json:"stage"`
}

func beginAutomationFactScan(ctx context.Context, db *gorm.DB, owner string, stages int, token string) (automationFactScan, error) {
	scan := automationFactScan{Version: 1, Owner: owner}
	if token == "" {
		err := db.WithContext(ctx).Model(&domain.LocalChangeJournal{}).
			Where("source_module = ? AND action IN ?", owner, []string{"automation_trigger", "event_outbox"}).
			Select("COALESCE(MAX(id),0)").Scan(&scan.Horizon).Error
		return scan, err
	}
	if len(token) > 512 {
		return scan, gorm.ErrInvalidData
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return scan, gorm.ErrInvalidData
	}
	scan = automationFactScan{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&scan) != nil || decoder.Decode(new(any)) != io.EOF || scan.Version != 1 || scan.Owner != owner || scan.Horizon == 0 || scan.AfterID > scan.Horizon || scan.Stage < 0 || scan.Stage >= stages {
		return scan, gorm.ErrInvalidData
	}
	return scan, nil
}

func (scan automationFactScan) next(stages int) string {
	if scan.Horizon == 0 || scan.Stage >= stages || (scan.Stage == stages-1 && scan.AfterID >= scan.Horizon) {
		return ""
	}
	// This fixed scalar struct cannot fail JSON encoding.
	raw, _ := json.Marshal(scan)
	return base64.RawURLEncoding.EncodeToString(raw)
}
