package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type ConversationRepository struct {
	db       *gorm.DB
	messages conversationMessagePort
	cleanup  conversationCleanupPort
	inboxes  conversationInboxPort
	contacts conversationContactPort
	users    conversationUserPort
	teams    conversationTeamPort
	labels   conversationLabelPort
	deleteMu sync.Mutex
	deleteID uint
}

type MacroConversationMutation struct {
	Status       *string
	Priority     *string
	SetAssignee  bool
	AssigneeID   *uint
	SetTeam      bool
	TeamID       *uint
	Muted        *bool
	SnoozedUntil *time.Time
	ClearSnooze  bool
}

func (r *ConversationRepository) ApplyMacroMutation(accountID, id uint, mutation MacroConversationMutation) error {
	updates := map[string]any{"last_activity_at": time.Now().UTC(), "object_version": gorm.Expr("object_version + 1"), "updated_at": time.Now().UTC()}
	if mutation.Status != nil {
		updates["status"] = *mutation.Status
	}
	if mutation.Priority != nil {
		updates["priority"] = *mutation.Priority
	}
	if mutation.SetAssignee {
		updates["assignee_id"] = mutation.AssigneeID
	}
	if mutation.SetTeam {
		updates["team_id"] = mutation.TeamID
	}
	if mutation.Muted != nil {
		updates["muted"] = *mutation.Muted
	}
	if mutation.SnoozedUntil != nil {
		updates["snoozed_until"] = mutation.SnoozedUntil
	}
	if mutation.ClearSnooze {
		updates["snoozed_until"] = nil
	}
	res := r.db.Model(&domain.Conversation{}).Where("account_id = ? AND id = ?", accountID, id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func NewConversationRepository(db *gorm.DB) *ConversationRepository {
	return NewConversationRepositoryWithPorts(db, NewMessageRepositoryWithConversationPort(db, nil), NewCombinedConversationCleanup(db))
}

type conversationMessagePort interface {
	IncomingUnreadWindow(accountID, conversationID uint) (time.Time, int64, bool, error)
	ListConversationAttachments(accountID, conversationID uint) ([]domain.Attachment, error)
}

func NewConversationRepositoryWithMessagePort(db *gorm.DB, messages conversationMessagePort) *ConversationRepository {
	return NewConversationRepositoryWithPorts(db, messages, NewCombinedConversationCleanup(db))
}

type conversationCleanupPort interface {
	CleanupConversation(accountID, conversationID uint) error
}

func NewConversationRepositoryWithPorts(db *gorm.DB, messages conversationMessagePort, cleanup conversationCleanupPort) *ConversationRepository {
	return NewConversationRepositoryWithReferences(db, messages, cleanup, NewInboxRepository(db), NewContactRepository(db))
}

type conversationInboxPort interface {
	Exists(accountID, id uint) (bool, error)
	FindSummaryByID(accountID, id uint) (*domain.Inbox, error)
}

type conversationContactPort interface {
	Exists(accountID, id uint) (bool, error)
	FindSummaryByID(accountID, id uint) (*domain.Contact, error)
}

type conversationUserPort interface {
	FindByID(id uint) (*domain.User, error)
}

type conversationTeamPort interface {
	FindSummaryByID(accountID, id uint) (*domain.Team, error)
}

type conversationLabelPort interface {
	FindByID(accountID, id uint) (*domain.Label, error)
	FindIDsByTitles(accountID uint, titles []any) ([]uint, error)
	Exists(accountID, id uint) (bool, error)
}

func NewConversationRepositoryWithReferences(db *gorm.DB, messages conversationMessagePort, cleanup conversationCleanupPort, inboxes conversationInboxPort, contacts conversationContactPort) *ConversationRepository {
	return NewConversationRepositoryWithReadPorts(db, messages, cleanup, inboxes, contacts, NewUserRepository(db), NewTeamRepository(db), NewLabelRepository(db))
}

func NewConversationRepositoryWithReadPorts(db *gorm.DB, messages conversationMessagePort, cleanup conversationCleanupPort, inboxes conversationInboxPort, contacts conversationContactPort, users conversationUserPort, teams conversationTeamPort, labels conversationLabelPort) *ConversationRepository {
	return &ConversationRepository{db: db, messages: messages, cleanup: cleanup, inboxes: inboxes, contacts: contacts, users: users, teams: teams, labels: labels}
}

// MessageTargetExists is the CON-owned query used before a MSG command.
// It deliberately reads no other module's table or preload association.
func (r *ConversationRepository) MessageTargetExists(accountID, conversationID uint) (bool, error) {
	var count int64
	err := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ? AND status <> ?", accountID, conversationID, domain.ConversationStatusDeleting).
		Count(&count).Error
	return count == 1, err
}

// ApplyMessageActivity accepts the stable MSG change ID as its idempotency key.
// A duplicate delivery cannot increment unread_count twice. CON owns both its
// state change and corresponding local change record.
func (r *ConversationRepository) ApplyMessageActivity(accountID, conversationID, actorID uint, incoming bool, occurredAt time.Time, messageChangeID string) error {
	if accountID == 0 || conversationID == 0 || messageChangeID == "" {
		return gorm.ErrInvalidData
	}
	activityChangeID := "con-activity-" + messageChangeID
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", activityChangeID).Count(&existing).Error; err != nil {
			return err
		}
		if existing != 0 {
			return nil
		}
		var conversation domain.Conversation
		if err := tx.Where("account_id = ? AND id = ? AND status <> ?", accountID, conversationID, domain.ConversationStatusDeleting).First(&conversation).Error; err != nil {
			return err
		}
		updates := map[string]any{"object_version": gorm.Expr("object_version + 1")}
		if conversation.LastActivityAt.Before(occurredAt) {
			updates["last_activity_at"] = occurredAt
		}
		if incoming {
			updates["unread_count"] = gorm.Expr("unread_count + 1")
		}
		result := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ? AND status <> ?", accountID, conversationID, domain.ConversationStatusDeleting).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		var version int
		if err := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ?", accountID, conversationID).Select("object_version").Scan(&version).Error; err != nil {
			return err
		}
		change := domain.LocalChangeJournal{
			ChangeID: activityChangeID, SourceModule: "CON", AccountID: accountID,
			ObjectType: "Conversation", ObjectID: conversationID,
			ObjectVersionBefore: version - 1, ObjectVersionAfter: version,
			Action: "update", ChangedFields: "last_activity_at,unread_count",
			ActorType: "system", ActorID: actorID, SourceType: "msg.message.created.v1",
			CommandID: messageChangeID, CorrelationID: messageChangeID, CausationID: messageChangeID,
			OccurredAt: occurredAt,
		}
		return NewJournalRepository(tx).RecordLocalChange(tx, &change)
	})
}

func (r *ConversationRepository) Create(c *domain.Conversation) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var maxDisplayID uint
		row := tx.Model(&domain.Conversation{}).
			Where("account_id = ?", c.AccountID).
			Select("COALESCE(MAX(display_id), 0)").
			Row()
		_ = row.Scan(&maxDisplayID)

		c.DisplayID = maxDisplayID + 1
		c.LastActivityAt = time.Now().UTC()

		return tx.Create(c).Error
	})
}

// CreateImported preserves a source display ID when one is supplied while
// keeping display-ID allocation inside the CON owner boundary.
func (r *ConversationRepository) CreateImported(c *domain.Conversation) error {
	if r.inboxes == nil || r.contacts == nil {
		return gorm.ErrInvalidData
	}
	inboxExists, err := r.inboxes.Exists(c.AccountID, c.InboxID)
	if err != nil {
		return err
	}
	contactExists, err := r.contacts.Exists(c.AccountID, c.ContactID)
	if err != nil {
		return err
	}
	if !inboxExists || !contactExists {
		return gorm.ErrRecordNotFound
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if c.DisplayID == 0 {
			var maxDisplayID uint
			row := tx.Model(&domain.Conversation{}).
				Where("account_id = ?", c.AccountID).
				Select("COALESCE(MAX(display_id), 0)").
				Row()
			_ = row.Scan(&maxDisplayID)
			c.DisplayID = maxDisplayID + 1
		}
		return tx.Create(c).Error
	})
}

func (r *ConversationRepository) FindByID(accountID, id uint) (*domain.Conversation, error) {
	var conv domain.Conversation
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&conv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if err := r.hydrateConversation(accountID, &conv); err != nil {
		return nil, err
	}
	return &conv, nil
}

func (r *ConversationRepository) hydrateConversation(accountID uint, conv *domain.Conversation) error {
	var err error
	if r.contacts != nil {
		conv.Contact, err = r.contacts.FindSummaryByID(accountID, conv.ContactID)
		if err != nil {
			return err
		}
	}
	if r.inboxes != nil {
		conv.Inbox, err = r.inboxes.FindSummaryByID(accountID, conv.InboxID)
		if err != nil {
			return err
		}
	}
	if conv.AssigneeID != nil && r.users != nil {
		conv.Assignee, err = r.users.FindByID(*conv.AssigneeID)
		if err != nil {
			return err
		}
	}
	if conv.TeamID != nil && r.teams != nil {
		conv.Team, err = r.teams.FindSummaryByID(accountID, *conv.TeamID)
		if err != nil {
			return err
		}
	}
	var links []domain.ConversationLabel
	if err := r.db.Where("conversation_id = ?", conv.ID).Find(&links).Error; err != nil {
		return err
	}
	if r.labels != nil {
		for _, link := range links {
			label, err := r.labels.FindByID(accountID, link.LabelID)
			if err != nil {
				return err
			}
			if label != nil {
				conv.Labels = append(conv.Labels, *label)
			}
		}
	}
	return nil
}

func (r *ConversationRepository) FindByUUID(uuid string) (*domain.Conversation, error) {
	var conversation domain.Conversation
	if err := r.db.Where("uuid = ?", uuid).First(&conversation).Error; err != nil {
		return nil, err
	}
	return &conversation, nil
}

func (r *ConversationRepository) FindGlobalByID(id uint) (*domain.Conversation, error) {
	var conversation domain.Conversation
	if err := r.db.First(&conversation, id).Error; err != nil {
		return nil, err
	}
	return &conversation, nil
}

func (r *ConversationRepository) FindForInbox(accountID, inboxID, conversationID uint) (*domain.Conversation, error) {
	var conversation domain.Conversation
	if err := r.db.Where("id = ? AND inbox_id = ? AND account_id = ?", conversationID, inboxID, accountID).First(&conversation).Error; err != nil {
		return nil, err
	}
	return &conversation, nil
}

func (r *ConversationRepository) FindOpenByContactAndInbox(accountID, contactID, inboxID uint) (*domain.Conversation, error) {
	var conv domain.Conversation
	err := r.db.
		Where("account_id = ? AND contact_id = ? AND inbox_id = ? AND status = ?", accountID, contactID, inboxID, domain.ConversationStatusOpen).
		Order("id DESC").
		First(&conv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if err := r.hydrateConversation(accountID, &conv); err != nil {
		return nil, err
	}
	return &conv, nil
}

func (r *ConversationRepository) List(accountID uint, status, priority string, assigneeID *uint, inboxID *uint, page, pageSize int) ([]domain.Conversation, int64, error) {
	var conversations []domain.Conversation
	var total int64

	query := r.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)

	if status != "" {
		query = query.Where("status = ?", status)
	}
	if priority != "" {
		query = query.Where("priority = ?", priority)
	}
	if assigneeID != nil {
		query = query.Where("assignee_id = ?", *assigneeID)
	}
	if inboxID != nil {
		query = query.Where("inbox_id = ?", *inboxID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.
		Offset(offset).Limit(pageSize).
		Order("last_activity_at DESC").
		Find(&conversations).Error

	if err != nil {
		return nil, 0, err
	}
	for i := range conversations {
		if err := r.hydrateConversation(accountID, &conversations[i]); err != nil {
			return nil, 0, err
		}
	}
	return conversations, total, nil
}

type ContactConversationStats struct {
	Total              int64
	Open               int64
	Resolved           int64
	ConversationIDs    []uint
	FirstInteractionAt *time.Time
	LastActivityAt     *time.Time
}

func (r *ConversationRepository) contactIDsQuery(accountID uint, contactIDs []uint) *gorm.DB {
	query := r.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)
	if len(contactIDs) == 0 {
		return query.Where("1 = 0")
	}
	return query.Where("contact_id IN ?", contactIDs)
}

func (r *ConversationRepository) FindIDsByContactIDs(accountID uint, contactIDs []uint) ([]uint, error) {
	var ids []uint
	err := r.contactIDsQuery(accountID, contactIDs).Pluck("id", &ids).Error
	return ids, err
}

func (r *ConversationRepository) ActiveContactIDs(accountID uint) ([]uint, error) {
	var ids []uint
	err := r.db.Model(&domain.Conversation{}).Where("account_id = ? AND status <> ? AND contact_id > 0", accountID, domain.ConversationStatusResolved).Distinct("contact_id").Pluck("contact_id", &ids).Error
	return ids, err
}

func (r *ConversationRepository) DetachContactIfInactive(accountID, contactID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var active int64
		if err := tx.Model(&domain.Conversation{}).Where("account_id = ? AND contact_id = ? AND status IN ?", accountID, contactID, []string{domain.ConversationStatusOpen, domain.ConversationStatusPending}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrContactHasActiveConversations
		}
		return tx.Model(&domain.Conversation{}).Where("account_id = ? AND contact_id = ?", accountID, contactID).Update("contact_id", 0).Error
	})
}

func (r *ConversationRepository) ReassignContact(accountID, fromContactID, toContactID uint) error {
	return r.db.Model(&domain.Conversation{}).Where("account_id = ? AND contact_id = ?", accountID, fromContactID).Update("contact_id", toContactID).Error
}

func (r *ConversationRepository) ListByContactIDs(accountID uint, contactIDs []uint, status string, inboxID, contactID *uint, page, pageSize int) ([]domain.Conversation, int64, error) {
	query := r.contactIDsQuery(accountID, contactIDs)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if inboxID != nil {
		query = query.Where("inbox_id = ?", *inboxID)
	}
	if contactID != nil {
		query = query.Where("contact_id = ?", *contactID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}
	var conversations []domain.Conversation
	if err := query.Order("last_activity_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&conversations).Error; err != nil {
		return nil, 0, err
	}
	for i := range conversations {
		if err := r.hydrateConversation(accountID, &conversations[i]); err != nil {
			return nil, 0, err
		}
	}
	return conversations, total, nil
}

func (r *ConversationRepository) GetStatsByContactIDs(accountID uint, contactIDs []uint) (*ContactConversationStats, error) {
	stats := &ContactConversationStats{}
	if err := r.contactIDsQuery(accountID, contactIDs).Count(&stats.Total).Error; err != nil {
		return nil, err
	}
	if err := r.contactIDsQuery(accountID, contactIDs).Where("status = ?", domain.ConversationStatusOpen).Count(&stats.Open).Error; err != nil {
		return nil, err
	}
	if err := r.contactIDsQuery(accountID, contactIDs).Where("status = ?", domain.ConversationStatusResolved).Count(&stats.Resolved).Error; err != nil {
		return nil, err
	}
	if err := r.contactIDsQuery(accountID, contactIDs).Pluck("id", &stats.ConversationIDs).Error; err != nil {
		return nil, err
	}
	var first, last domain.Conversation
	if err := r.contactIDsQuery(accountID, contactIDs).Order("created_at ASC").First(&first).Error; err == nil {
		stats.FirstInteractionAt = &first.CreatedAt
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err := r.contactIDsQuery(accountID, contactIDs).Order("last_activity_at DESC").First(&last).Error; err == nil {
		stats.LastActivityAt = &last.LastActivityAt
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return stats, nil
}

func (r *ConversationRepository) captainQuery(accountID uint, inboxIDs, messageConversationIDs []uint, start, end time.Time) *gorm.DB {
	q := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND created_at >= ? AND created_at <= ?", accountID, start, end)
	if len(inboxIDs) > 0 && len(messageConversationIDs) > 0 {
		return q.Where("inbox_id IN ? OR id IN ?", inboxIDs, messageConversationIDs)
	}
	if len(inboxIDs) > 0 {
		return q.Where("inbox_id IN ?", inboxIDs)
	}
	if len(messageConversationIDs) > 0 {
		return q.Where("id IN ?", messageConversationIDs)
	}
	return q.Where("1 = 0")
}

func (r *ConversationRepository) GetCaptainStats(accountID uint, inboxIDs, messageConversationIDs []uint, start, end time.Time) (int64, int64, int64, error) {
	var handled, resolved, handoff int64
	if err := r.captainQuery(accountID, inboxIDs, messageConversationIDs, start, end).Count(&handled).Error; err != nil {
		return 0, 0, 0, err
	}
	if err := r.captainQuery(accountID, inboxIDs, messageConversationIDs, start, end).Where("status = ?", domain.ConversationStatusResolved).Count(&resolved).Error; err != nil {
		return 0, 0, 0, err
	}
	if err := r.captainQuery(accountID, inboxIDs, messageConversationIDs, start, end).Where("assignee_id IS NOT NULL AND assignee_id > 0").Count(&handoff).Error; err != nil {
		return 0, 0, 0, err
	}
	return handled, resolved, handoff, nil
}

func (r *ConversationRepository) ListCaptainConversations(accountID uint, inboxIDs, messageConversationIDs []uint, start, end time.Time, metric string, page, perPage int) ([]domain.Conversation, int64, error) {
	q := r.captainQuery(accountID, inboxIDs, messageConversationIDs, start, end)
	switch metric {
	case "auto_resolution_rate":
		q = q.Where("status = ?", domain.ConversationStatusResolved)
	case "handoff_rate":
		q = q.Where("assignee_id IS NOT NULL AND assignee_id > 0")
	case "reopen_rate":
		q = q.Where("status = ?", domain.ConversationStatusOpen)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var conversations []domain.Conversation
	if err := q.Order("id DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&conversations).Error; err != nil {
		return nil, 0, err
	}
	for i := range conversations {
		if err := r.hydrateConversation(accountID, &conversations[i]); err != nil {
			return nil, 0, err
		}
	}
	return conversations, total, nil
}

func (r *ConversationRepository) Update(c *domain.Conversation) error {
	return r.db.Save(c).Error
}

func (r *ConversationRepository) UpdateStatus(accountID, id uint, status string, snoozedUntil *time.Time) error {
	updates := map[string]any{
		"status":           status,
		"snoozed_until":    snoozedUntil,
		"last_activity_at": time.Now().UTC(),
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

func (r *ConversationRepository) Assign(accountID, id uint, assigneeID *uint) error {
	updates := map[string]any{
		"assignee_id":      assigneeID,
		"last_activity_at": time.Now().UTC(),
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

func (r *ConversationRepository) AssignTeam(accountID, id uint, teamID *uint) error {
	updates := map[string]any{
		"team_id":          teamID,
		"last_activity_at": time.Now().UTC(),
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

// CountActiveAssigned is the CON-owned workload query used by routing. Label
// titles are resolved through the CUS port before the CON-local relation is
// applied, so callers never query CON or CUS storage directly.
func (r *ConversationRepository) CountActiveAssigned(accountID, inboxID, assigneeID, excludeID uint, activeSince *time.Time, excludedLabelTitles []string) (int64, error) {
	q := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND assignee_id = ? AND status != ?", accountID, assigneeID, domain.ConversationStatusResolved)
	if inboxID > 0 {
		q = q.Where("inbox_id = ?", inboxID)
	}
	if excludeID > 0 {
		q = q.Where("id != ?", excludeID)
	}
	if activeSince != nil {
		q = q.Where("(last_activity_at IS NOT NULL AND last_activity_at >= ?) OR (last_activity_at IS NULL AND created_at >= ?)", *activeSince, *activeSince)
	}
	if len(excludedLabelTitles) > 0 {
		if r.labels == nil {
			return 0, gorm.ErrInvalidData
		}
		titles := make([]any, len(excludedLabelTitles))
		for i := range excludedLabelTitles {
			titles[i] = excludedLabelTitles[i]
		}
		labelIDs, err := r.labels.FindIDsByTitles(accountID, titles)
		if err != nil {
			return 0, err
		}
		if len(labelIDs) > 0 {
			q = q.Where("id NOT IN (SELECT conversation_id FROM conversation_labels WHERE label_id IN ?)", labelIDs)
		}
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *ConversationRepository) LastAssignedAgent(accountID, inboxID, excludeID uint) (uint, error) {
	var conversation domain.Conversation
	q := r.db.Select("id", "assignee_id").
		Where("account_id = ? AND inbox_id = ? AND assignee_id IS NOT NULL", accountID, inboxID)
	if excludeID > 0 {
		q = q.Where("id != ?", excludeID)
	}
	err := q.Order("id DESC").First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil || conversation.AssigneeID == nil {
		return 0, err
	}
	return *conversation.AssigneeID, nil
}

// ResumeExpiredSnoozed owns both the CON scan and conditional state change.
// The guarded update makes repeated or concurrent scheduler runs idempotent.
func (r *ConversationRepository) ResumeExpiredSnoozed(ctx context.Context, now time.Time) ([]domain.Conversation, error) {
	var candidates []domain.Conversation
	if err := r.db.WithContext(ctx).
		Where("status = ? AND snoozed_until IS NOT NULL AND snoozed_until <= ?", domain.ConversationStatusSnoozed, now).
		Order("id ASC").Find(&candidates).Error; err != nil {
		return nil, err
	}

	resumed := make([]domain.Conversation, 0, len(candidates))
	for i := range candidates {
		conversation := &candidates[i]
		result := r.db.WithContext(ctx).Model(&domain.Conversation{}).
			Where("account_id = ? AND id = ? AND status = ? AND snoozed_until IS NOT NULL AND snoozed_until <= ?", conversation.AccountID, conversation.ID, domain.ConversationStatusSnoozed, now).
			Updates(map[string]any{
				"status":           domain.ConversationStatusOpen,
				"snoozed_until":    nil,
				"last_activity_at": now,
			})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		conversation.Status = domain.ConversationStatusOpen
		conversation.SnoozedUntil = nil
		conversation.LastActivityAt = now
		resumed = append(resumed, *conversation)
	}
	return resumed, nil
}

func (r *ConversationRepository) MoveInbox(accountID, sourceInboxID, targetInboxID uint) error {
	var sourceCount, targetCount int64
	if err := r.db.Model(&domain.Inbox{}).Where("account_id = ? AND id = ?", accountID, sourceInboxID).Count(&sourceCount).Error; err != nil {
		return err
	}
	if err := r.db.Model(&domain.Inbox{}).Where("account_id = ? AND id = ?", accountID, targetInboxID).Count(&targetCount).Error; err != nil {
		return err
	}
	if sourceCount != 1 || targetCount != 1 {
		return gorm.ErrRecordNotFound
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND inbox_id = ?", accountID, sourceInboxID).
		Update("inbox_id", targetInboxID).Error
}

func (r *ConversationRepository) AssignWithTeam(accountID, id uint, hasAssignee bool, assigneeID *uint, hasTeam bool, teamID *uint) error {
	updates := map[string]any{
		"last_activity_at": time.Now().UTC(),
	}
	if hasAssignee {
		if assigneeID != nil && *assigneeID > 0 {
			updates["assignee_id"] = assigneeID
		} else {
			updates["assignee_id"] = nil
		}
	}
	if hasTeam {
		if teamID != nil && *teamID > 0 {
			updates["team_id"] = teamID
		} else {
			updates["team_id"] = nil
		}
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

func (r *ConversationRepository) TouchActivity(accountID, id uint) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("last_activity_at", time.Now().UTC()).Error
}

func (r *ConversationRepository) UpdatePriority(accountID, id uint, priority string) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(map[string]any{
			"priority":         priority,
			"last_activity_at": time.Now().UTC(),
		}).Error
}

func (r *ConversationRepository) ApplySLAPolicy(accountID, id, policyID uint) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("sla_policy_id", policyID).Error
}

func (r *ConversationRepository) UpdateSLATiming(accountID, id uint, updates map[string]any) error {
	allowed := map[string]bool{"first_response_due_at": true, "next_response_due_at": true, "resolution_due_at": true, "sla_status": true}
	filtered := make(map[string]any, len(updates))
	for key, value := range updates {
		if allowed[key] {
			filtered[key] = value
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(filtered).Error
}

// ResetSLAProjection clears the CON-owned SLA projection after RPT removes the
// applied SLA aggregate.
func (r *ConversationRepository) ResetSLAProjection(accountID, id uint) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(map[string]any{
			"sla_policy_id": nil, "sla_status": "active",
			"first_response_due_at": nil, "next_response_due_at": nil, "resolution_due_at": nil,
		}).Error
}

// FindIDsForAppliedSLAFilter resolves RPT's conversation filters inside CON.
// Label titles are resolved through the CUS owner port.
func (r *ConversationRepository) FindIDsForAppliedSLAFilter(accountID uint, inboxID, teamID, assigneeID *uint, labelTitle string) ([]uint, error) {
	q := r.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)
	if inboxID != nil && *inboxID > 0 {
		q = q.Where("inbox_id = ?", *inboxID)
	}
	if teamID != nil && *teamID > 0 {
		q = q.Where("team_id = ?", *teamID)
	}
	if assigneeID != nil && *assigneeID > 0 {
		q = q.Where("assignee_id = ?", *assigneeID)
	}
	if labelTitle != "" {
		if r.labels == nil {
			return nil, gorm.ErrInvalidData
		}
		labelIDs, err := r.labels.FindIDsByTitles(accountID, []any{labelTitle})
		if err != nil {
			return nil, err
		}
		if len(labelIDs) == 0 {
			return []uint{}, nil
		}
		q = q.Where("id IN (SELECT conversation_id FROM conversation_labels WHERE label_id IN ?)", labelIDs)
	}
	var ids []uint
	if err := q.Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// UpdateLastSeen updates agent read position and clears unread count
func (r *ConversationRepository) UpdateLastSeen(accountID, id, userID uint, seenAt time.Time) error {
	var conv domain.Conversation
	if err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&conv).Error; err != nil {
		return err
	}

	updates := map[string]any{
		"agent_last_seen_at": seenAt,
		"unread_count":       0,
	}
	if conv.AssigneeID != nil && *conv.AssigneeID == userID {
		updates["assignee_last_seen_at"] = seenAt
	}

	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

// MarkUnread rewinds the agent's read position before the latest incoming message
func (r *ConversationRepository) MarkUnread(accountID, id uint) error {
	if r.messages == nil {
		return gorm.ErrInvalidData
	}
	latestAt, unreadCount, found, err := r.messages.IncomingUnreadWindow(accountID, id)
	if err != nil {
		return err
	}
	updates := map[string]any{}
	if found {
		seenAt := latestAt.Add(-1 * time.Second)
		if unreadCount < 1 {
			unreadCount = 1
		}
		updates["agent_last_seen_at"] = seenAt
		updates["unread_count"] = int(unreadCount)
	} else {
		updates["agent_last_seen_at"] = nil
		updates["unread_count"] = 1
	}

	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

// UpdateContactLastSeen updates the customer's read position
func (r *ConversationRepository) UpdateContactLastSeen(accountID, id uint, seenAt time.Time) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("contact_last_seen_at", seenAt).Error
}

// ToggleMute updates conversation mute state
func (r *ConversationRepository) ToggleMute(accountID, id uint, muted bool) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("muted", muted).Error
}

// AttachLabel records the CON-owned relationship between a conversation and a
// CUS-owned label. The account guard prevents cross-tenant relation writes.
func (r *ConversationRepository) AttachLabel(accountID, conversationID, labelID uint) error {
	if r.labels == nil {
		return gorm.ErrInvalidData
	}
	labelExists, err := r.labels.Exists(accountID, labelID)
	if err != nil {
		return err
	}
	if !labelExists {
		return gorm.ErrRecordNotFound
	}
	return r.AttachLabelReference(accountID, conversationID, labelID)
}

// AttachLabelReference writes the CON-owned relationship after the caller has
// validated the CUS-owned label.
func (r *ConversationRepository) AttachLabelReference(accountID, conversationID, labelID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var conversationCount int64
		if err := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ?", accountID, conversationID).Count(&conversationCount).Error; err != nil {
			return err
		}
		if conversationCount != 1 {
			return gorm.ErrRecordNotFound
		}
		relation := domain.ConversationLabel{ConversationID: conversationID, LabelID: labelID}
		return tx.Where(relation).FirstOrCreate(&relation).Error
	})
}

// DetachLabel removes a CON-owned label relationship with tenant guards on
// the CON-owned relationship for an account-scoped conversation.
func (r *ConversationRepository) DetachLabel(accountID, conversationID, labelID uint) error {
	return r.db.Where(
		"conversation_id IN (?) AND label_id = ?",
		r.db.Model(&domain.Conversation{}).Select("id").Where("account_id = ? AND id = ?", accountID, conversationID),
		labelID,
	).Delete(&domain.ConversationLabel{}).Error
}

// FindLabelIDs returns CON-owned relationship data for an account-scoped conversation.
func (r *ConversationRepository) FindLabelIDs(accountID, conversationID uint) ([]uint, error) {
	var conversationCount int64
	if err := r.db.Model(&domain.Conversation{}).Where("account_id = ? AND id = ?", accountID, conversationID).Count(&conversationCount).Error; err != nil {
		return nil, err
	}
	if conversationCount != 1 {
		return nil, gorm.ErrRecordNotFound
	}
	var labelIDs []uint
	err := r.db.Model(&domain.ConversationLabel{}).Where("conversation_id = ?", conversationID).Pluck("label_id", &labelIDs).Error
	return labelIDs, err
}

// DeleteLabelReferences removes only relations attached to conversations owned
// by the supplied account, leaving other tenants untouched.
func (r *ConversationRepository) DeleteLabelReferences(accountID, labelID uint) error {
	return r.db.Where(
		"conversation_id IN (?) AND label_id = ?",
		r.db.Model(&domain.Conversation{}).Select("id").Where("account_id = ?", accountID),
		labelID,
	).Delete(&domain.ConversationLabel{}).Error
}

// Delete coordinates idempotent cleanup commands from each owning module. A
// durable CON marker lets a later retry resume if one owner is unavailable.
func (r *ConversationRepository) Delete(accountID, id uint) error {
	if accountID == 0 || id == 0 || r.cleanup == nil {
		return gorm.ErrInvalidData
	}
	changeID := fmt.Sprintf("con-delete-%d-%d", accountID, id)
	if err := r.db.Transaction(func(tx *gorm.DB) error {
		var conversation domain.Conversation
		if err := tx.Where("account_id = ? AND id = ?", accountID, id).First(&conversation).Error; err != nil {
			return err
		}
		var existing int64
		if err := tx.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", changeID).Count(&existing).Error; err != nil {
			return err
		}
		if existing != 0 {
			return nil
		}
		if err := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ?", accountID, id).
			Updates(map[string]any{"status": domain.ConversationStatusDeleting, "object_version": gorm.Expr("object_version + 1")}).Error; err != nil {
			return err
		}
		change := domain.LocalChangeJournal{
			ChangeID: changeID, SourceModule: "CON", AccountID: accountID,
			ObjectType: "Conversation", ObjectID: id,
			ObjectVersionBefore: conversation.ObjectVersion, ObjectVersionAfter: conversation.ObjectVersion + 1,
			Action: "delete", ChangedFields: "status", ActorType: "system",
			SourceType: "con.conversation.delete.v1", CommandID: changeID,
			CorrelationID: changeID, Status: "pending",
		}
		return NewJournalRepository(tx).RecordLocalChange(tx, &change)
	}); err != nil {
		return err
	}
	if err := r.cleanup.CleanupConversation(accountID, id); err != nil {
		return err
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("conversation_id = ?", id).Delete(&domain.ConversationLabel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("conversation_id = ?", id).Delete(&domain.ConversationParticipant{}).Error; err != nil {
			return err
		}
		result := tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Conversation{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", changeID).Update("status", "delivered").Error
	})
}

// ReconcilePendingDeletes resumes owner cleanup after a process or dependency
// failure. Idempotent owner commands make replay safe.
func (r *ConversationRepository) ReconcilePendingDeletes(limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		return 0, gorm.ErrInvalidData
	}
	r.deleteMu.Lock()
	defer r.deleteMu.Unlock()
	var pending []domain.Conversation
	query := func(after uint) *gorm.DB {
		q := r.db.Where("status = ?", domain.ConversationStatusDeleting)
		if after != 0 {
			q = q.Where("id > ?", after)
		}
		return q.Order("id ASC").Limit(limit)
	}
	if err := query(r.deleteID).Find(&pending).Error; err != nil {
		return 0, err
	}
	if len(pending) == 0 && r.deleteID != 0 {
		r.deleteID = 0
		if err := query(0).Find(&pending).Error; err != nil {
			return 0, err
		}
	}
	if len(pending) != 0 {
		r.deleteID = pending[len(pending)-1].ID
	}
	completed := 0
	var firstError error
	for _, conversation := range pending {
		if err := r.Delete(conversation.AccountID, conversation.ID); err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		completed++
	}
	return completed, firstError
}

// GetMeta calculates conversation count summaries across dimensions
func (r *ConversationRepository) GetMeta(accountID uint, status string, inboxID, teamID *uint, currentUserID uint) (map[string]int64, error) {
	if status == "" {
		status = domain.ConversationStatusOpen
	}

	baseQuery := func() *gorm.DB {
		q := r.db.Model(&domain.Conversation{}).Where("account_id = ? AND status = ?", accountID, status)
		if inboxID != nil && *inboxID > 0 {
			q = q.Where("inbox_id = ?", *inboxID)
		}
		if teamID != nil && *teamID > 0 {
			q = q.Where("team_id = ?", *teamID)
		}
		return q
	}

	var mineCount, unassignedCount, allCount, assignedCount int64

	if err := baseQuery().Where("assignee_id = ?", currentUserID).Count(&mineCount).Error; err != nil {
		return nil, err
	}
	if err := baseQuery().Where("assignee_id IS NULL").Count(&unassignedCount).Error; err != nil {
		return nil, err
	}
	if err := baseQuery().Count(&allCount).Error; err != nil {
		return nil, err
	}
	if err := baseQuery().Where("assignee_id IS NOT NULL").Count(&assignedCount).Error; err != nil {
		return nil, err
	}

	return map[string]int64{
		"mine_count":       mineCount,
		"unassigned_count": unassignedCount,
		"all_count":        allCount,
		"assigned_count":   assignedCount,
	}, nil
}

// GetUnreadCounts returns aggregated unread conversation counts
func (r *ConversationRepository) GetUnreadCounts(accountID, currentUserID uint) (map[string]int64, error) {
	var totalUnread, mineUnread, unassignedUnread int64

	row := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND status = ?", accountID, domain.ConversationStatusOpen).
		Select("COALESCE(SUM(unread_count), 0)").
		Row()
	_ = row.Scan(&totalUnread)

	rowMine := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND status = ? AND assignee_id = ?", accountID, domain.ConversationStatusOpen, currentUserID).
		Select("COALESCE(SUM(unread_count), 0)").
		Row()
	_ = rowMine.Scan(&mineUnread)

	rowUnassigned := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND status = ? AND assignee_id IS NULL", accountID, domain.ConversationStatusOpen).
		Select("COALESCE(SUM(unread_count), 0)").
		Row()
	_ = rowUnassigned.Scan(&unassignedUnread)

	return map[string]int64{
		"total":                   totalUnread,
		"mine":                    mineUnread,
		"unassigned":              unassignedUnread,
		"total_unread_count":      totalUnread,
		"mine_unread_count":       mineUnread,
		"unassigned_unread_count": unassignedUnread,
	}, nil
}

// Filter searches conversations matching flexible structured rules
func (r *ConversationRepository) Filter(accountID uint, filters []domain.FilterRule, page, pageSize int) ([]domain.Conversation, int64, error) {
	var conversations []domain.Conversation
	var total int64

	query := r.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)

	for _, rule := range filters {
		op := strings.ToLower(rule.FilterOperator)
		key := strings.ToLower(rule.AttributeKey)
		switch key {
		case "status":
			if op == "equal_to" {
				query = query.Where("status IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("status NOT IN (?)", rule.Values)
			}
		case "priority":
			if op == "equal_to" {
				query = query.Where("priority IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("priority NOT IN (?)", rule.Values)
			}
		case "assignee_id":
			switch op {
			case "is_present":
				query = query.Where("assignee_id IS NOT NULL")
			case "is_not_present":
				query = query.Where("assignee_id IS NULL")
			case "equal_to":
				query = query.Where("assignee_id IN (?)", rule.Values)
			case "not_equal_to":
				query = query.Where("assignee_id NOT IN (?)", rule.Values)
			}
		case "inbox_id":
			if op == "equal_to" {
				query = query.Where("inbox_id IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("inbox_id NOT IN (?)", rule.Values)
			}
		case "team_id":
			switch op {
			case "is_present":
				query = query.Where("team_id IS NOT NULL")
			case "is_not_present":
				query = query.Where("team_id IS NULL")
			case "equal_to":
				query = query.Where("team_id IN (?)", rule.Values)
			case "not_equal_to":
				query = query.Where("team_id NOT IN (?)", rule.Values)
			}
		case "contact_id":
			if op == "equal_to" {
				query = query.Where("contact_id IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("contact_id NOT IN (?)", rule.Values)
			}
		case "labels":
			if op == "equal_to" || op == "contains" {
				if r.labels == nil {
					return nil, 0, gorm.ErrInvalidData
				}
				ids, err := r.labels.FindIDsByTitles(accountID, rule.Values)
				if err != nil {
					return nil, 0, err
				}
				query = query.Where("id IN (SELECT conversation_id FROM conversation_labels WHERE label_id IN ?)", ids)
			}
		case "custom_attributes":
			for _, v := range rule.Values {
				query = query.Where("custom_attributes LIKE ?", fmt.Sprintf("%%%v%%", v))
			}
		}
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.
		Offset(offset).Limit(pageSize).
		Order("last_activity_at DESC, id DESC").
		Find(&conversations).Error

	if err != nil {
		return nil, 0, err
	}
	for i := range conversations {
		if err := r.hydrateConversation(accountID, &conversations[i]); err != nil {
			return nil, 0, err
		}
	}
	return conversations, total, nil
}

// UpdateCustomAttributes updates or merges key-values into conversation custom attributes
func (r *ConversationRepository) UpdateCustomAttributes(accountID, id uint, attrs map[string]any) (map[string]any, error) {
	var conv domain.Conversation
	if err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&conv).Error; err != nil {
		return nil, err
	}

	merged := make(map[string]any)
	if conv.CustomAttributes != "" {
		_ = json.Unmarshal([]byte(conv.CustomAttributes), &merged)
	}
	for k, v := range attrs {
		merged[k] = v
	}

	bytes, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}

	conv.CustomAttributes = string(bytes)
	conv.LastActivityAt = time.Now().UTC()
	if err := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(map[string]any{
			"custom_attributes": conv.CustomAttributes,
			"last_activity_at":  conv.LastActivityAt,
		}).Error; err != nil {
		return nil, err
	}

	return merged, nil
}

// ListAttachments returns all attachments belonging to messages in the conversation
func (r *ConversationRepository) ListAttachments(accountID, conversationID uint) ([]domain.Attachment, error) {
	if r.messages == nil {
		return nil, gorm.ErrInvalidData
	}
	return r.messages.ListConversationAttachments(accountID, conversationID)
}
