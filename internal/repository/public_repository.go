package repository

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// PublicRepository composes customer-facing API views through module owners.
type PublicRepository struct {
	inboxes       *InboxRepository
	contacts      *ContactRepository
	conversations *ConversationRepository
	messages      *MessageRepository
}

func NewPublicRepository(db *gorm.DB) *PublicRepository {
	return NewPublicRepositoryWithOwnerPorts(NewInboxRepository(db), NewContactRepository(db), NewConversationRepository(db), NewMessageRepository(db))
}

func NewPublicRepositoryWithOwnerPorts(inboxes *InboxRepository, contacts *ContactRepository, conversations *ConversationRepository, messages *MessageRepository) *PublicRepository {
	return &PublicRepository{inboxes: inboxes, contacts: contacts, conversations: conversations, messages: messages}
}

func (r *PublicRepository) FindInboxByIdentifier(ctx context.Context, identifier string) (*domain.Inbox, error) {
	value := strings.TrimSpace(identifier)
	if value == "" {
		return nil, errors.New("inbox identifier is required")
	}
	if id, err := strconv.ParseUint(value, 10, 64); err == nil && id > 0 {
		return r.inboxes.FindByGlobalID(uint(id))
	}
	return r.inboxes.FindByWebsiteToken(value)
}

func (r *PublicRepository) ResolveContact(ctx context.Context, inboxID, accountID uint, idOrSourceID string) (*domain.Contact, *domain.ContactInbox, error) {
	return r.contacts.ResolvePublic(accountID, inboxID, idOrSourceID)
}

func (r *PublicRepository) UpdateContact(ctx context.Context, accountID, contactID uint, updates map[string]any, customAttrs map[string]any) (*domain.Contact, error) {
	return r.contacts.UpdatePublic(accountID, contactID, updates, customAttrs)
}

func (r *PublicRepository) ListContactConversations(ctx context.Context, accountID, inboxID, contactID uint, status string, page, pageSize int) ([]domain.Conversation, int64, error) {
	filter := strings.TrimSpace(status)
	if filter == "all" {
		filter = ""
	}
	conversations, total, err := r.conversations.ListByContactIDs(accountID, []uint{contactID}, filter, &inboxID, &contactID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	for i := range conversations {
		messages, _, err := r.messages.ListPublicByConversation(accountID, conversations[i].ID, 0, 0, 1, 100)
		if err != nil {
			return nil, 0, err
		}
		conversations[i].Messages = messages
	}
	return conversations, total, nil
}

func (r *PublicRepository) GetContactConversation(ctx context.Context, accountID, inboxID, contactID, convID uint) (*domain.Conversation, error) {
	owned, err := r.conversations.FindForInbox(accountID, inboxID, convID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if owned.ContactID != contactID {
		return nil, nil
	}
	conversation, err := r.conversations.FindByID(accountID, convID)
	if err != nil || conversation == nil {
		return conversation, err
	}
	messages, _, err := r.messages.ListPublicByConversation(accountID, convID, 0, 0, 1, 100)
	if err != nil {
		return nil, err
	}
	conversation.Messages = messages
	return conversation, nil
}

func (r *PublicRepository) ListConversationMessages(ctx context.Context, accountID, convID uint, beforeID, afterID uint, page, pageSize int) ([]domain.Message, int64, error) {
	return r.messages.ListPublicByConversation(accountID, convID, beforeID, afterID, page, pageSize)
}

func (r *PublicRepository) UpdateConversationStatus(ctx context.Context, accountID, convID uint, newStatus string) (*domain.Conversation, error) {
	if err := r.conversations.UpdateStatus(accountID, convID, newStatus, nil); err != nil {
		return nil, err
	}
	return r.conversations.FindByID(accountID, convID)
}

func (r *PublicRepository) MergeConversationCustomAttributes(ctx context.Context, accountID, convID uint, newAttrs map[string]any) (map[string]any, error) {
	return r.conversations.UpdateCustomAttributes(accountID, convID, newAttrs)
}
