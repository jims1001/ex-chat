package service

import "time"

// MigrationContactItem represents an imported contact record
type MigrationContactItem struct {
	ID               any        `json:"id"` // can be string or number (legacy id)
	Name             string     `json:"name"`
	Email            string     `json:"email"`
	PhoneNumber      string     `json:"phone_number"`
	Phone            string     `json:"phone"`
	Identifier       string     `json:"identifier"`
	CustomAttributes string     `json:"custom_attributes"`
	CreatedAt        *time.Time `json:"created_at"`
}

// MigrationConversationItem represents an imported conversation record
type MigrationConversationItem struct {
	ID                any                    `json:"id"` // legacy conversation id
	ContactID         any                    `json:"contact_id"`
	ContactEmail      string                 `json:"contact_email"`
	ContactIdentifier string                 `json:"contact_identifier"`
	ContactName       string                 `json:"contact_name"`
	InboxID           any                    `json:"inbox_id"`
	Status            string                 `json:"status"`   // open, resolved, snoozed, pending
	Priority          string                 `json:"priority"` // low, medium, high, urgent
	DisplayID         uint                   `json:"display_id"`
	CreatedAt         *time.Time             `json:"created_at"`
	Messages          []MigrationMessageItem `json:"messages"`
}

// MigrationMessageItem represents an imported message record
type MigrationMessageItem struct {
	ID             any                       `json:"id"` // legacy message id
	ConversationID any                       `json:"conversation_id"`
	SenderType     string                    `json:"sender_type"` // Contact, User
	SenderID       uint                      `json:"sender_id"`
	MessageType    string                    `json:"message_type"` // incoming, outgoing, activity
	ContentType    string                    `json:"content_type"` // text
	Content        string                    `json:"content"`
	Private        bool                      `json:"private"`
	CreatedAt      *time.Time                `json:"created_at"`
	Attachments    []MigrationAttachmentItem `json:"attachments"`
}

// MigrationAttachmentItem represents an imported attachment record
type MigrationAttachmentItem struct {
	ID        any        `json:"id"`
	MessageID any        `json:"message_id"`
	FileType  string     `json:"file_type"`
	DataURL   string     `json:"data_url"`
	FileURL   string     `json:"file_url"`
	FileSize  int64      `json:"file_size"`
	CreatedAt *time.Time `json:"created_at"`
}

// MigrationBundle holds all migration resources in a single payload
type MigrationBundle struct {
	Contacts      []MigrationContactItem      `json:"contacts"`
	Conversations []MigrationConversationItem `json:"conversations"`
	Messages      []MigrationMessageItem      `json:"messages"`
	Attachments   []MigrationAttachmentItem   `json:"attachments"`
}

// MigrationStats tracks count of migrated resources and errors
type MigrationStats struct {
	ContactsCount      int      `json:"contacts_count"`
	ConversationsCount int      `json:"conversations_count"`
	MessagesCount      int      `json:"messages_count"`
	AttachmentsCount   int      `json:"attachments_count"`
	Errors             []string `json:"errors"`
}

// TotalProcessed returns sum of all successfully migrated records
func (s *MigrationStats) TotalProcessed() int {
	return s.ContactsCount + s.ConversationsCount + s.MessagesCount + s.AttachmentsCount
}
