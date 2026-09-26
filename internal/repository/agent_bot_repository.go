package repository

import (
	"context"
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// AgentBotRepository manages automated webhook bot agents
type AgentBotRepository struct {
	db *gorm.DB
}

func NewAgentBotRepository(db *gorm.DB) *AgentBotRepository {
	return &AgentBotRepository{db: db}
}

func (r *AgentBotRepository) Create(ctx context.Context, bot *domain.AgentBot) error {
	return r.db.WithContext(ctx).Create(bot).Error
}

func (r *AgentBotRepository) List(ctx context.Context, accountID uint) ([]domain.AgentBot, error) {
	var list []domain.AgentBot
	err := r.db.WithContext(ctx).Where("account_id = ?", accountID).Order("id ASC").Find(&list).Error
	return list, err
}

func (r *AgentBotRepository) GetByID(ctx context.Context, accountID, botID uint) (*domain.AgentBot, error) {
	var bot domain.AgentBot
	err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, botID).First(&bot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &bot, nil
}

func (r *AgentBotRepository) Update(ctx context.Context, accountID, botID uint, updates map[string]any) (*domain.AgentBot, error) {
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return nil, err
	}
	if bot == nil {
		return nil, gorm.ErrRecordNotFound
	}

	if len(updates) > 0 {
		if err := r.db.WithContext(ctx).Model(&domain.AgentBot{}).
			Where("account_id = ? AND id = ?", accountID, botID).
			Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	return r.GetByID(ctx, accountID, botID)
}

func (r *AgentBotRepository) Delete(ctx context.Context, accountID, botID uint) error {
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return err
	}
	if bot == nil {
		return gorm.ErrRecordNotFound
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Clean up any inbox associations
		if err := tx.Where("account_id = ? AND agent_bot_id = ?", accountID, botID).
			Delete(&domain.AgentBotInbox{}).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ? AND id = ?", accountID, botID).
			Delete(&domain.AgentBot{}).Error
	})
}

func (r *AgentBotRepository) DeleteAvatar(ctx context.Context, accountID, botID uint) error {
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return err
	}
	if bot == nil {
		return gorm.ErrRecordNotFound
	}

	return r.db.WithContext(ctx).Model(&domain.AgentBot{}).
		Where("account_id = ? AND id = ?", accountID, botID).
		Update("avatar_url", "").Error
}

func (r *AgentBotRepository) ListInboxIDs(ctx context.Context, accountID, botID uint) ([]uint, error) {
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return nil, err
	}
	if bot == nil {
		return nil, gorm.ErrRecordNotFound
	}

	var ids []uint
	err = r.db.WithContext(ctx).Model(&domain.AgentBotInbox{}).
		Where("account_id = ? AND agent_bot_id = ?", accountID, botID).
		Order("inbox_id ASC").Pluck("inbox_id", &ids).Error
	return ids, err
}

func (r *AgentBotRepository) BindInbox(ctx context.Context, accountID, botID, inboxID uint) error {
	// Verify bot belongs to account
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return err
	}
	if bot == nil {
		return gorm.ErrRecordNotFound
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// An inbox can only have one agent bot at a time
		if err := tx.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).
			Delete(&domain.AgentBotInbox{}).Error; err != nil {
			return err
		}
		binding := domain.AgentBotInbox{
			AccountID:  accountID,
			AgentBotID: botID,
			InboxID:    inboxID,
		}
		return tx.Create(&binding).Error
	})
}

func (r *AgentBotRepository) UnbindInbox(ctx context.Context, accountID, botID, inboxID uint) error {
	res := r.db.WithContext(ctx).
		Where("account_id = ? AND agent_bot_id = ? AND inbox_id = ?", accountID, botID, inboxID).
		Delete(&domain.AgentBotInbox{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AgentBotRepository) GetByInbox(ctx context.Context, accountID, inboxID uint) (*domain.AgentBot, error) {
	var binding domain.AgentBotInbox
	err := r.db.WithContext(ctx).Where("account_id = ? AND inbox_id = ?", accountID, inboxID).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.GetByID(ctx, accountID, binding.AgentBotID)
}

func (r *AgentBotRepository) ReplaceInboxBinding(ctx context.Context, accountID, inboxID, botID uint) error {
	if botID > 0 {
		bot, err := r.GetByID(ctx, accountID, botID)
		if err != nil {
			return err
		}
		if bot == nil {
			return gorm.ErrRecordNotFound
		}
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Delete(&domain.AgentBotInbox{}).Error; err != nil {
			return err
		}
		if botID == 0 {
			return nil
		}
		return tx.Create(&domain.AgentBotInbox{AccountID: accountID, AgentBotID: botID, InboxID: inboxID}).Error
	})
}
