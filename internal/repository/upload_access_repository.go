package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/uploadpath"
)

var ErrUploadOwnership = errors.New("file ownership cannot be established")

type UploadOwner struct {
	AccountID      uint
	InboxID        uint
	ContactID      uint
	ConversationID uint
}

// UploadVisitorIdentity revalidates the entire signed identity against durable
// channel data. Removed/remapped contacts and ambiguous source mappings fail shut.
func (r *AttachmentRepository) UploadVisitorIdentity(ctx context.Context, inboxID, contactID uint, sourceID string) (*UploadOwner, error) {
	if inboxID == 0 || contactID == 0 || sourceID == "" {
		return nil, ErrUploadOwnership
	}
	if r.contacts == nil {
		return nil, ErrUploadOwnership
	}
	mapping, err := r.contacts.FindUploadIdentity(ctx, inboxID, contactID, sourceID)
	if err != nil || mapping == nil || mapping.ContactID != contactID {
		return nil, ErrUploadOwnership
	}
	return r.widgetUploadOwner(ctx, inboxID, contactID)
}

func (r *AttachmentRepository) widgetUploadOwner(ctx context.Context, inboxID, contactID uint) (*UploadOwner, error) {
	if r.inboxes == nil || r.contacts == nil {
		return nil, ErrUploadOwnership
	}
	inbox, err := r.inboxes.FindByGlobalID(inboxID)
	if err != nil || inbox == nil {
		return nil, ErrUploadOwnership
	}
	contact, err := r.contacts.FindSummaryByID(inbox.AccountID, contactID)
	if err != nil || contact == nil {
		return nil, ErrUploadOwnership
	}
	mapping, err := r.contacts.FindUploadIdentity(ctx, inboxID, contactID, "")
	if err != nil || mapping == nil {
		return nil, ErrUploadOwnership
	}
	return &UploadOwner{AccountID: inbox.AccountID, InboxID: inboxID, ContactID: contactID}, nil
}

// UploadAccount determines an agent's tenant only from exact namespace syntax.
func (r *AttachmentRepository) UploadAccount(ctx context.Context, key string) (uint, error) {
	if _, err := uploadpath.CanonicalKey(key); err != nil {
		return 0, err
	}
	parts := strings.Split(key, "/")
	if len(parts) == 3 && parts[0] == "widget" || len(parts) == 4 && parts[0] == "widget" {
		inboxID, err := uploadpath.NamespaceID(parts[1], "inbox_")
		if err != nil {
			return 0, err
		}
		if len(parts) == 4 {
			contactID, err := uploadpath.NamespaceID(parts[2], "contact_")
			if err != nil {
				return 0, err
			}
			owner, err := r.widgetUploadOwner(ctx, inboxID, contactID)
			if err != nil {
				return 0, err
			}
			return owner.AccountID, nil
		}
		if r.inboxes == nil {
			return 0, ErrUploadOwnership
		}
		inbox, err := r.inboxes.FindByGlobalID(inboxID)
		if err != nil || inbox == nil {
			return 0, ErrUploadOwnership
		}
		return inbox.AccountID, nil
	}
	if len(parts) >= 2 && strings.HasPrefix(parts[0], "account_") {
		return uploadpath.NamespaceID(parts[0], "account_")
	}
	return 0, ErrUploadOwnership
}

// VisitorUploadOwner uses the contact namespace for pre-message direct uploads.
// Older unnamespaced files require exact persisted attachment metadata. Never
// infer ownership from a basename, an inbox alone, or a client-provided hint.
func (r *AttachmentRepository) VisitorUploadOwner(ctx context.Context, key string) (*UploadOwner, error) {
	if _, err := uploadpath.CanonicalKey(key); err != nil {
		return nil, err
	}
	parts := strings.Split(key, "/")
	if len(parts) == 4 && parts[0] == "widget" {
		inboxID, err := uploadpath.NamespaceID(parts[1], "inbox_")
		if err != nil {
			return nil, err
		}
		contactID, err := uploadpath.NamespaceID(parts[2], "contact_")
		if err != nil {
			return nil, err
		}
		return r.widgetUploadOwner(ctx, inboxID, contactID)
	}
	accountID, err := r.UploadAccount(ctx, key)
	if err != nil {
		return nil, err
	}
	var attachments []domain.Attachment
	// Compare complete origin-relative paths, including the older unescaped
	// representation used for literal spaces. Neither form is a basename match.
	// Unknown/external representations require a per-file signed URL.
	urls := []string{uploadpath.URL(key)}
	// A raw percent sign could instead be an escape for a different file. Only
	// the escaped representation can prove ownership of a literal-percent key.
	if !strings.Contains(key, "%") {
		urls = append(urls, "/uploads/"+key)
	}
	if err := r.db.WithContext(ctx).Where("data_url IN ?", urls).Find(&attachments).Error; err != nil {
		return nil, err
	}
	if len(attachments) == 0 {
		return nil, ErrUploadOwnership
	}
	var owner *UploadOwner
	for _, att := range attachments {
		current, visible, err := r.attachmentOwner(ctx, &att)
		if err != nil || !visible || current.AccountID != accountID {
			return nil, ErrUploadOwnership
		}
		if parts[0] == "widget" {
			inboxID, err := uploadpath.NamespaceID(parts[1], "inbox_")
			if err != nil || current.InboxID != inboxID {
				return nil, ErrUploadOwnership
			}
		} else if len(parts) >= 3 && strings.HasPrefix(parts[1], "conv_") {
			convID, err := uploadpath.NamespaceID(parts[1], "conv_")
			if err != nil || current.ConversationID != convID {
				return nil, ErrUploadOwnership
			}
		}
		if owner != nil && (owner.AccountID != current.AccountID || owner.InboxID != current.InboxID || owner.ContactID != current.ContactID) {
			return nil, ErrUploadOwnership
		}
		owner = current
	}
	return owner, nil
}

func (r *AttachmentRepository) attachmentOwner(ctx context.Context, att *domain.Attachment) (*UploadOwner, bool, error) {
	if r.messages == nil || r.conversations == nil || r.contacts == nil || r.inboxes == nil {
		return nil, false, ErrUploadOwnership
	}
	message, err := r.messages.FindByID(att.AccountID, att.MessageID)
	if err != nil || message == nil {
		return nil, false, ErrUploadOwnership
	}
	conv, err := r.conversations.FindSummaryByID(att.AccountID, message.ConversationID)
	if err != nil || conv == nil {
		return nil, false, ErrUploadOwnership
	}
	inbox, err := r.inboxes.FindSummaryByID(att.AccountID, conv.InboxID)
	if err != nil || inbox == nil {
		return nil, false, ErrUploadOwnership
	}
	contact, err := r.contacts.FindSummaryByID(att.AccountID, conv.ContactID)
	if err != nil || contact == nil {
		return nil, false, ErrUploadOwnership
	}
	return &UploadOwner{AccountID: att.AccountID, InboxID: conv.InboxID, ContactID: conv.ContactID, ConversationID: conv.ID}, !message.Private && !message.Deleted && message.DeletedAt == nil, nil
}

// validateLocalAttachment prevents forging ownership metadata by attaching a
// known local file URL to a different conversation/contact/account.
func (r *AttachmentRepository) validateLocalAttachment(ctx context.Context, att *domain.Attachment) error {
	key, local, err := uploadpath.LocalURLKey(att.DataURL)
	if err != nil {
		return ErrUploadOwnership
	}
	if !local {
		return nil
	}
	target, _, err := r.attachmentOwner(ctx, att)
	if err != nil {
		return err
	}
	parts := strings.Split(key, "/")
	if len(parts) == 3 && strings.HasPrefix(parts[0], "account_") && strings.HasPrefix(parts[1], "conv_") {
		accountID, e1 := uploadpath.NamespaceID(parts[0], "account_")
		convID, e2 := uploadpath.NamespaceID(parts[1], "conv_")
		if e1 != nil || e2 != nil || accountID != target.AccountID || convID != target.ConversationID {
			return ErrUploadOwnership
		}
	} else {
		owner, err := r.VisitorUploadOwner(ctx, key)
		if err != nil || owner.AccountID != target.AccountID || owner.InboxID != target.InboxID || owner.ContactID != target.ContactID {
			return ErrUploadOwnership
		}
	}
	// Persist one exact URL form, stripping credentials/expiry query parameters.
	att.DataURL = uploadpath.URL(key)
	return nil
}
