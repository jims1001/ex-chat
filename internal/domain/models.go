package domain

import (
	"time"
)

// User role constants
const (
	RoleAdministrator = "administrator"
	RoleAgent         = "agent"
)

// Availability constants
const (
	AvailabilityOnline  = "online"
	AvailabilityOffline = "offline"
	AvailabilityBusy    = "busy"
)

// Channel type constants
const (
	ChannelWebWidget = "Channel::WebWidget"
	ChannelApi       = "Channel::Api"
	ChannelEmail     = "Channel::Email"
)

// Conversation status constants
const (
	ConversationStatusOpen     = "open"
	ConversationStatusResolved = "resolved"
	ConversationStatusPending  = "pending"
	ConversationStatusSnoozed  = "snoozed"
)

// Conversation priority constants
const (
	PriorityLow    = "low"
	PriorityMedium = "medium"
	PriorityHigh   = "high"
	PriorityUrgent = "urgent"
)

// Message type constants
const (
	MessageTypeIncoming = "incoming"
	MessageTypeOutgoing = "outgoing"
	MessageTypeActivity = "activity"
	MessageTypeTemplate = "template"
)

// Message content type constants
const (
	ContentTypeText = "text"
	ContentTypeForm = "form"
)

// Message status constants
const (
	MessageStatusSent      = "sent"
	MessageStatusDelivered = "delivered"
	MessageStatusRead      = "read"
	MessageStatusFailed    = "failed"
)

// Sender type constants
const (
	SenderTypeUser    = "User"
	SenderTypeContact = "Contact"
)

// Account represents a tenant workspace
type Account struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	Name                string    `gorm:"size:255;not null" json:"name"`
	Locale              string    `gorm:"size:10;default:'en'" json:"locale"`
	Domain              string    `gorm:"size:255" json:"domain"`
	SupportEmail        string    `gorm:"size:255" json:"support_email"`
	AutoResolveDuration int       `gorm:"default:0" json:"auto_resolve_duration"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// User represents a system operator, agent, or administrator
type User struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Name         string    `gorm:"size:255;not null" json:"name"`
	Email        string    `gorm:"size:255;uniqueIndex;not null" json:"email"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	Role         string    `gorm:"size:50;default:'agent'" json:"role"`
	Availability string    `gorm:"size:50;default:'online'" json:"availability"`
	AvatarURL    string    `gorm:"size:512" json:"avatar_url"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AccountUser associates a user with a tenant account
type AccountUser struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	AccountID    uint      `gorm:"index;not null" json:"account_id"`
	UserID       uint      `gorm:"index;not null" json:"user_id"`
	Role         string    `gorm:"size:50;default:'agent'" json:"role"`
	Availability string    `gorm:"size:50;default:'online'" json:"availability"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	Account *Account `gorm:"foreignKey:AccountID" json:"account,omitempty"`
	User    *User    `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// Inbox represents a communication channel for customer interactions
type Inbox struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	AccountID           uint      `gorm:"index;not null" json:"account_id"`
	Name                string    `gorm:"size:255;not null" json:"name"`
	ChannelType         string    `gorm:"size:50;not null;default:'Channel::WebWidget'" json:"channel_type"`
	WebsiteToken        string    `gorm:"size:128;uniqueIndex;not null" json:"website_token"`
	GreetingMessage     string    `gorm:"type:text" json:"greeting_message"`
	GreetingEnabled     bool      `gorm:"default:true" json:"greeting_enabled"`
	WorkingHoursEnabled bool      `gorm:"default:false" json:"working_hours_enabled"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`

	Members []User `gorm:"many2many:inbox_members;" json:"members,omitempty"`
}

// InboxMember links an agent to an inbox
type InboxMember struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	InboxID   uint      `gorm:"uniqueIndex:idx_inbox_user;not null" json:"inbox_id"`
	UserID    uint      `gorm:"uniqueIndex:idx_inbox_user;not null" json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Contact represents a customer profile in an account
type Contact struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	AccountID        uint      `gorm:"index;not null" json:"account_id"`
	Name             string    `gorm:"size:255" json:"name"`
	Email            string    `gorm:"size:255;index" json:"email"`
	PhoneNumber      string    `gorm:"size:50;index" json:"phone_number"`
	Identifier       string    `gorm:"size:255;index" json:"identifier"`
	AvatarURL        string    `gorm:"size:512" json:"avatar_url"`
	CustomAttributes string    `gorm:"type:text" json:"custom_attributes"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ContactInbox maps a contact's identity in a specific channel
type ContactInbox struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ContactID uint      `gorm:"uniqueIndex:idx_contact_inbox;not null" json:"contact_id"`
	InboxID   uint      `gorm:"uniqueIndex:idx_contact_inbox;not null" json:"inbox_id"`
	SourceID  string    `gorm:"size:255;index;not null" json:"source_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Contact *Contact `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Inbox   *Inbox   `gorm:"foreignKey:InboxID" json:"inbox,omitempty"`
}

// Conversation represents a chat thread between a contact and agents
type Conversation struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	DisplayID        uint       `gorm:"index;not null" json:"display_id"`
	AccountID        uint       `gorm:"index;not null" json:"account_id"`
	InboxID          uint       `gorm:"index;not null" json:"inbox_id"`
	ContactID        uint       `gorm:"index;not null" json:"contact_id"`
	AssigneeID       *uint      `gorm:"index" json:"assignee_id"`
	Status           string     `gorm:"size:50;default:'open';index" json:"status"`
	Priority         string     `gorm:"size:50;default:'medium'" json:"priority"`
	CustomAttributes string     `gorm:"type:text" json:"custom_attributes"`
	SnoozedUntil     *time.Time `json:"snoozed_until"`
	LastActivityAt   time.Time  `gorm:"index" json:"last_activity_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`

	Contact  *Contact `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Inbox    *Inbox   `gorm:"foreignKey:InboxID" json:"inbox,omitempty"`
	Assignee *User    `gorm:"foreignKey:AssigneeID" json:"assignee,omitempty"`
	Labels   []Label  `gorm:"many2many:conversation_labels;" json:"labels,omitempty"`
}

// Message represents an individual text or event entry in a conversation
type Message struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	ConversationID uint      `gorm:"index;not null" json:"conversation_id"`
	SenderType     string    `gorm:"size:50;not null" json:"sender_type"`
	SenderID       uint      `gorm:"index;not null" json:"sender_id"`
	MessageType    string    `gorm:"size:50;default:'incoming'" json:"message_type"`
	ContentType    string    `gorm:"size:50;default:'text'" json:"content_type"`
	Content        string    `gorm:"type:text;not null" json:"content"`
	Private        bool      `gorm:"default:false" json:"private"`
	Status         string    `gorm:"size:50;default:'sent'" json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	Sender any `gorm:"-" json:"sender,omitempty"`
}

// Label represents a tag that can be attached to conversations
type Label struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccountID   uint      `gorm:"index;not null" json:"account_id"`
	Title       string    `gorm:"size:100;not null" json:"title"`
	Description string    `gorm:"size:255" json:"description"`
	Color       string    `gorm:"size:50;default:'#1f93ff'" json:"color"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ConversationLabel join table
type ConversationLabel struct {
	ConversationID uint `gorm:"primaryKey" json:"conversation_id"`
	LabelID        uint `gorm:"primaryKey" json:"label_id"`
}

// CannedResponse represents a quick reply shortcut for agents
type CannedResponse struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	AccountID uint      `gorm:"index;not null" json:"account_id"`
	ShortCode string    `gorm:"size:100;index;not null" json:"short_code"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
