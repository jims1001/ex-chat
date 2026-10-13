package modruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClaimAutomationMode makes executor ownership a database-wide choice. It never
// changes an existing claim: switching requires an explicit offline migration
// after all old instances have stopped. Old binaries cannot enforce this claim.
func ClaimAutomationMode(ctx context.Context, db *gorm.DB, mode string) error {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "legacy"
	}
	if mode != "legacy" && mode != "durable" {
		return fmt.Errorf("invalid automation execution mode %q", mode)
	}
	claim := domain.LocalChangeJournal{ChangeID: "aut-monolith-execution-mode-v1", SourceModule: "AUT", Action: "automation_execution_mode", AfterState: mode, Status: "delivered", Result: "applied"}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&claim).Error; err != nil {
		return err
	}
	var stored domain.LocalChangeJournal
	if err := db.WithContext(ctx).Where("change_id = ?", claim.ChangeID).First(&stored).Error; err != nil {
		return err
	}
	if stored.SourceModule != "AUT" || stored.Action != "automation_execution_mode" || stored.AfterState != mode {
		return fmt.Errorf("automation execution mode conflicts with persisted ownership; stop all instances before an explicit offline mode migration")
	}
	return nil
}
