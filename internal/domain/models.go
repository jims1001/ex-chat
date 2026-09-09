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

// Strategy type constants
const (
	StrategyRoundRobin  = "round_robin"
	StrategyLeastActive = "least_active"
)

// Sentiment constants
const (
	SentimentNeutral    = "neutral"
	SentimentPositive   = "positive"
	SentimentFrustrated = "frustrated"
)

// Automation event and action constants
const (
	EventConversationCreated = "conversation_created"
	EventConversationUpdated = "conversation_updated"
	EventMessageCreated      = "message_created"

	ActionAssignTeam          = "assign_team"
	ActionAssignAgent         = "assign_agent"
	ActionRemoveAssignedAgent = "remove_assigned_agent"
	ActionRemoveAssignedTeam  = "remove_assigned_team"
	ActionSendMessage         = "send_message"
	ActionAddLabel            = "add_label"
	ActionAddLabels           = "add_labels"
	ActionRemoveLabel         = "remove_label"
	ActionRemoveLabels        = "remove_labels"
	ActionResolveConv         = "resolve_conversation"
	ActionMuteConv            = "mute_conversation"
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
	Role                  string               `gorm:"size:50;default:'agent'" json:"role"`
	Availability          string               `gorm:"size:50;default:'online'" json:"availability"`
	AgentCapacityPolicyID *uint                `gorm:"index" json:"agent_capacity_policy_id"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`

	Account             *Account             `gorm:"foreignKey:AccountID" json:"account,omitempty"`
	User                *User                `gorm:"foreignKey:UserID" json:"user,omitempty"`
	AgentCapacityPolicy *AgentCapacityPolicy `gorm:"foreignKey:AgentCapacityPolicyID" json:"agent_capacity_policy,omitempty"`
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
	OutOfOfficeMessage  string    `gorm:"type:text" json:"out_of_office_message"`
	Timezone            string    `gorm:"size:100;default:'UTC'" json:"timezone"`
	WorkingHours        string    `gorm:"type:text" json:"working_hours"` // JSON array of WorkingHourConfig
	AssignmentPolicyID  *uint     `gorm:"index" json:"assignment_policy_id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`

	Members          []User            `gorm:"many2many:inbox_members;" json:"members,omitempty"`
	AssignmentPolicy *AssignmentPolicy `gorm:"foreignKey:AssignmentPolicyID" json:"assignment_policy,omitempty"`
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
	CompanyID        *uint     `gorm:"index" json:"company_id"`
	Name             string    `gorm:"size:255" json:"name"`
	Email            string    `gorm:"size:255;index" json:"email"`
	PhoneNumber      string    `gorm:"size:50;index" json:"phone_number"`
	Identifier       string    `gorm:"size:255;index" json:"identifier"`
	AvatarURL        string    `gorm:"size:512" json:"avatar_url"`
	CustomAttributes string    `gorm:"type:text" json:"custom_attributes"`
	ObjectVersion    int       `gorm:"default:1" json:"object_version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	Company *Company `gorm:"foreignKey:CompanyID" json:"company,omitempty"`
	Labels  []Label  `gorm:"many2many:contact_labels;" json:"labels,omitempty"`
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
	TeamID           *uint      `gorm:"index" json:"team_id"`
	SLAPolicyID      *uint      `gorm:"index" json:"sla_policy_id"`
	Status           string     `gorm:"size:50;default:'open';index" json:"status"`
	Priority         string     `gorm:"size:50;default:'medium'" json:"priority"`
	SLAStatus        string     `gorm:"size:50;default:'active'" json:"sla_status"`
	CustomAttributes string     `gorm:"type:text" json:"custom_attributes"`
	ObjectVersion    int        `gorm:"default:1" json:"object_version"`
	SnoozedUntil       *time.Time `json:"snoozed_until"`
	LastActivityAt     time.Time  `gorm:"index" json:"last_activity_at"`
	AgentLastSeenAt    *time.Time `json:"agent_last_seen_at"`
	ContactLastSeenAt  *time.Time `json:"contact_last_seen_at"`
	AssigneeLastSeenAt *time.Time `json:"assignee_last_seen_at"`
	UnreadCount        int        `gorm:"default:0" json:"unread_count"`
	Muted              bool       `gorm:"default:false" json:"muted"`
	FirstResponseDueAt *time.Time `json:"first_response_due_at,omitempty"`
	NextResponseDueAt  *time.Time `json:"next_response_due_at,omitempty"`
	ResolutionDueAt    *time.Time `json:"resolution_due_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`

	Contact    *Contact    `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Inbox      *Inbox      `gorm:"foreignKey:InboxID" json:"inbox,omitempty"`
	Assignee   *User       `gorm:"foreignKey:AssigneeID" json:"assignee,omitempty"`
	Team       *Team       `gorm:"foreignKey:TeamID" json:"team,omitempty"`
	Labels     []Label     `gorm:"many2many:conversation_labels;" json:"labels,omitempty"`
	AppliedSLA *AppliedSLA `gorm:"foreignKey:ConversationID" json:"applied_sla,omitempty"`
	SLAEvents  []SLAEvent  `gorm:"foreignKey:ConversationID" json:"sla_events,omitempty"`
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
	Status         string     `gorm:"size:50;default:'sent'" json:"status"`
	EchoID         string     `gorm:"size:255;index" json:"echo_id"`
	Deleted        bool       `gorm:"default:false;index" json:"deleted"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	EditedAt       *time.Time `json:"edited_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

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

// ContactLabel join table between contacts and labels
type ContactLabel struct {
	ContactID uint `gorm:"primaryKey" json:"contact_id"`
	LabelID   uint `gorm:"primaryKey" json:"label_id"`
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

// Team represents a group of support agents
type Team struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	AccountID       uint      `gorm:"index;not null" json:"account_id"`
	Name            string    `gorm:"size:255;not null" json:"name"`
	Description     string    `gorm:"type:text" json:"description"`
	AllowAutoAssign bool      `gorm:"default:true" json:"allow_auto_assign"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	Members []User `gorm:"many2many:team_members;" json:"members,omitempty"`
}

// TeamMember join table
type TeamMember struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TeamID    uint      `gorm:"uniqueIndex:idx_team_user;not null" json:"team_id"`
	UserID    uint      `gorm:"uniqueIndex:idx_team_user;not null" json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// CapacityPolicy defines maximum active conversation workload per agent
type CapacityPolicy struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	AccountID         uint      `gorm:"index;not null" json:"account_id"`
	UserID            uint      `gorm:"uniqueIndex;not null" json:"user_id"`
	ConversationLimit int       `gorm:"default:10" json:"conversation_limit"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// AgentCapacityPolicy represents an enterprise-grade agent capacity policy with per-inbox limits
type AgentCapacityPolicy struct {
	ID             uint                 `gorm:"primaryKey" json:"id"`
	AccountID      uint                 `gorm:"index;not null" json:"account_id"`
	Name           string               `gorm:"size:255;not null" json:"name"`
	Description    string               `gorm:"type:text" json:"description"`
	ExclusionRules string               `gorm:"type:text" json:"exclusion_rules"` // JSON config for older than hours, excluded labels
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`

	InboxCapacityLimits []InboxCapacityLimit `gorm:"foreignKey:AgentCapacityPolicyID" json:"inbox_capacity_limits,omitempty"`
	AccountUsers        []AccountUser        `gorm:"foreignKey:AgentCapacityPolicyID" json:"account_users,omitempty"`
	UsersCount          int                  `gorm:"-" json:"users_count,omitempty"`
}

// InboxCapacityLimit defines a specific conversation limit for an inbox under a capacity policy
type InboxCapacityLimit struct {
	ID                    uint      `gorm:"primaryKey" json:"id"`
	AgentCapacityPolicyID uint      `gorm:"uniqueIndex:idx_cap_policy_inbox;not null" json:"agent_capacity_policy_id"`
	InboxID               uint      `gorm:"uniqueIndex:idx_cap_policy_inbox;not null" json:"inbox_id"`
	ConversationLimit     int       `gorm:"not null;default:0" json:"conversation_limit"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`

	Inbox               *Inbox               `gorm:"foreignKey:InboxID" json:"inbox,omitempty"`
	AgentCapacityPolicy *AgentCapacityPolicy `gorm:"foreignKey:AgentCapacityPolicyID" json:"agent_capacity_policy,omitempty"`
}

// AssignmentPolicy defines an independent conversation routing and distribution strategy
type AssignmentPolicy struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	AccountID          uint      `gorm:"index;not null" json:"account_id"`
	Name               string    `gorm:"size:255;not null" json:"name"`
	Description        string    `gorm:"size:500" json:"description"`
	StrategyType       string    `gorm:"size:50;not null;default:'round_robin'" json:"strategy_type"` // round_robin, least_active, workload
	Enabled            bool      `gorm:"default:true" json:"enabled"`
	WorkingHoursOnly   bool      `gorm:"default:false" json:"working_hours_only"`
	AgentCapacityLimit int       `gorm:"default:0" json:"agent_capacity_limit"` // 0 = unlimited or fallback to user capacity
	FallbackAssigneeID *uint     `gorm:"index" json:"fallback_assignee_id"`
	FallbackTeamID     *uint     `gorm:"index" json:"fallback_team_id"`
	RuleConfig         string    `gorm:"type:text" json:"rule_config"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`

	FallbackAssignee   *User     `gorm:"foreignKey:FallbackAssigneeID" json:"fallback_assignee,omitempty"`
	FallbackTeam       *Team     `gorm:"foreignKey:FallbackTeamID" json:"fallback_team,omitempty"`
}

// CustomAttributeDefinition defines custom fields for Contact or Conversation
type CustomAttributeDefinition struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	AccountID            uint      `gorm:"index;not null" json:"account_id"`
	AttributeDisplayName string    `gorm:"size:255;not null" json:"attribute_display_name"`
	AttributeKey         string    `gorm:"size:100;not null" json:"attribute_key"`
	AttributeModel       string    `gorm:"size:50;not null" json:"attribute_model"` // contact_attribute, conversation_attribute
	AttributeDisplayType string    `gorm:"size:50;default:'text'" json:"attribute_display_type"`
	AttributeDescription string    `gorm:"type:text" json:"attribute_description,omitempty"`
	AttributeValues      string    `gorm:"type:text" json:"attribute_values,omitempty"`
	DefaultValue         string    `gorm:"size:255" json:"default_value"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// ContactNote represents an internal operator note attached to a customer profile
type ContactNote struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	AccountID uint      `gorm:"index;not null" json:"account_id"`
	ContactID uint      `gorm:"index;not null" json:"contact_id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// AutomationRule defines event-triggered automation workflow
type AutomationRule struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccountID   uint      `gorm:"index;not null" json:"account_id"`
	Name        string    `gorm:"size:255;not null" json:"name"`
	Description string    `gorm:"type:text" json:"description"`
	EventName   string    `gorm:"size:100;not null" json:"event_name"` // conversation_created, message_created
	Conditions  string    `gorm:"type:text" json:"conditions"`        // JSON condition rules
	Actions     string    `gorm:"type:text" json:"actions"`           // JSON action steps
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ----------------- HELP 帮助中心 -----------------

// Portal represents a Help Center portal
type Portal struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	AccountID    uint      `gorm:"index;not null" json:"account_id"`
	Name         string    `gorm:"size:255;not null" json:"name"`
	Slug         string    `gorm:"size:128;uniqueIndex;not null" json:"slug"`
	CustomDomain string    `gorm:"size:255" json:"custom_domain"`
	Color        string    `gorm:"size:50;default:'#1f93ff'" json:"color"`
	HeaderText   string    `gorm:"size:255" json:"header_text"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	Categories []Category `gorm:"foreignKey:PortalID" json:"categories,omitempty"`
}

// Category represents a grouping of knowledge articles
type Category struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	PortalID    uint      `gorm:"index;not null" json:"portal_id"`
	AccountID   uint      `gorm:"index;not null" json:"account_id"`
	Name        string    `gorm:"size:255;not null" json:"name"`
	Slug        string    `gorm:"size:128;not null" json:"slug"`
	Description string    `gorm:"type:text" json:"description"`
	Icon        string    `gorm:"size:100" json:"icon"`
	Position    int       `gorm:"default:0" json:"position"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	Articles []Article `gorm:"foreignKey:CategoryID" json:"articles,omitempty"`
}

// Article represents a documentation or FAQ article
type Article struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	PortalID   uint      `gorm:"index;not null" json:"portal_id"`
	CategoryID uint      `gorm:"index;not null" json:"category_id"`
	AccountID  uint      `gorm:"index;not null" json:"account_id"`
	AuthorID   uint      `gorm:"index;not null" json:"author_id"`
	Title      string    `gorm:"size:255;not null" json:"title"`
	Slug       string    `gorm:"size:255;not null" json:"slug"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	Status     string    `gorm:"size:50;default:'draft'" json:"status"` // draft, published, archived
	Views      int64     `gorm:"default:0" json:"views"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	Category *Category `gorm:"foreignKey:CategoryID" json:"category,omitempty"`
	Author   *User     `gorm:"foreignKey:AuthorID" json:"author,omitempty"`
}

// ----------------- EXT 插件与 Webhook -----------------

// Webhook defines an external webhook endpoint subscribed to account events
type Webhook struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	AccountID     uint      `gorm:"index;not null" json:"account_id"`
	URL           string    `gorm:"size:1024;not null" json:"url"`
	Secret        string    `gorm:"size:255" json:"secret"`
	Subscriptions string    `gorm:"type:text;not null" json:"subscriptions"` // JSON array of event names
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DashboardApp represents an embedded external application in the agent dashboard
type DashboardApp struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	AccountID  uint      `gorm:"index;not null" json:"account_id"`
	Title      string    `gorm:"size:255;not null" json:"title"`
	ContentURL string    `gorm:"size:1024;not null" json:"content_url"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// WebhookDelivery records webhook execution attempts and retries
type WebhookDelivery struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	AccountID    uint      `gorm:"index;not null" json:"account_id"`
	WebhookID    uint      `gorm:"index;not null" json:"webhook_id"`
	Event        string    `gorm:"size:64;not null" json:"event"`
	URL          string    `gorm:"size:1024;not null" json:"url"`
	Payload      string    `gorm:"type:text" json:"payload"`
	ResponseCode int       `json:"response_code"`
	ResponseBody string    `gorm:"type:text" json:"response_body,omitempty"`
	Attempts     int       `gorm:"default:1" json:"attempts"`
	Status       string    `gorm:"size:50;default:'delivered'" json:"status"` // delivered, dead_letter
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}

// IntegrationInstallation represents installed third-party apps for an account
type IntegrationInstallation struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	AccountID uint      `gorm:"index;not null" json:"account_id"`
	AppID     string    `gorm:"size:64;not null" json:"app_id"`
	Status    string    `gorm:"size:50;default:'installed'" json:"status"` // installed, disabled
	Settings  string    `gorm:"type:text" json:"settings,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SLABreachLog records SLA breach events for resolution or first response
type SLABreachLog struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	AccountID        uint      `gorm:"index;not null" json:"account_id"`
	ConversationID   uint      `gorm:"index;not null" json:"conversation_id"`
	SLAPolicyID      uint      `gorm:"index;not null" json:"sla_policy_id"`
	BreachType       string    `gorm:"size:50;not null" json:"breach_type"` // first_response, resolution
	ThresholdSeconds int       `json:"threshold_seconds"`
	ActualSeconds    int       `json:"actual_seconds"`
	CreatedAt        time.Time `gorm:"index" json:"created_at"`
}

// CampaignDelivery records message delivery to audience contact
type CampaignDelivery struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	CampaignID     uint      `gorm:"index;not null" json:"campaign_id"`
	ContactID      uint      `gorm:"index;not null" json:"contact_id"`
	ConversationID uint      `json:"conversation_id"`
	Status         string    `gorm:"size:50;default:'sent'" json:"status"`
	ErrorMessage   string    `gorm:"size:255" json:"error_message,omitempty"`
	SentAt         time.Time `json:"sent_at"`
}

// PushDeliveryLog records mobile/web push delivery attempts
type PushDeliveryLog struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	UserID         uint      `gorm:"index;not null" json:"user_id"`
	DeviceToken    string    `gorm:"size:255;not null" json:"device_token"`
	Platform       string    `gorm:"size:50;not null" json:"platform"` // fcm, apns, webpush
	Title          string    `gorm:"size:255" json:"title"`
	Body           string    `gorm:"type:text" json:"body"`
	Status         string    `gorm:"size:50;default:'delivered'" json:"status"` // delivered, failed
	HTTPStatusCode int       `json:"http_status_code"`
	ErrorDetail    string    `gorm:"size:255" json:"error_detail,omitempty"`
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
}

// ----------------- ENT 企业与配置 -----------------

// SystemConfig stores global installation-level configurations
type SystemConfig struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ConfigKey string    `gorm:"size:100;uniqueIndex;not null" json:"config_key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AccountFeature toggles enterprise feature availability for an account
type AccountFeature struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccountID   uint      `gorm:"uniqueIndex:idx_acc_feature;not null" json:"account_id"`
	FeatureName string    `gorm:"uniqueIndex:idx_acc_feature;size:100;not null" json:"feature_name"`
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ----------------- OPS 宏与快捷能力 -----------------

// Macro defines pre-set canned multi-action execution templates
type Macro struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	AccountID  uint      `gorm:"index;not null" json:"account_id"`
	Name       string    `gorm:"size:255;not null" json:"name"`
	Visibility string    `gorm:"size:50;default:'personal'" json:"visibility"` // personal, global
	CreatedBy  uint      `gorm:"index" json:"created_by"`
	Actions    string    `gorm:"type:text;not null" json:"actions"` // JSON array of action definitions
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Notification records internal agent notifications
type Notification struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	AccountID          uint       `gorm:"index;not null" json:"account_id"`
	UserID             uint       `gorm:"index;not null" json:"user_id"`
	NotificationType   string     `gorm:"size:100;not null" json:"notification_type"` // e.g. "conversation_assignment", "conversation_mention"
	PrimaryActorType   string     `gorm:"size:50" json:"primary_actor_type"`
	PrimaryActorID     uint       `json:"primary_actor_id"`
	SecondaryActorType string     `gorm:"size:50" json:"secondary_actor_type"`
	SecondaryActorID   uint       `json:"secondary_actor_id"`
	ReadAt             *time.Time `json:"read_at"`
	SnoozedUntil       *time.Time `gorm:"index" json:"snoozed_until,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

const (
	NotificationTypeConversationAssignment = "conversation_assignment"
	NotificationTypeConversationMention    = "conversation_mention"
	NotificationTypeConversationCreation   = "conversation_creation"
	NotificationTypeSLABreach              = "sla_breach"
	NotificationTypeSystemAlert            = "system_alert"
)

// NotificationSetting records user notification preferences and channel subscriptions
type NotificationSetting struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	AccountID          uint      `gorm:"uniqueIndex:idx_notif_acc_user;not null" json:"account_id"`
	UserID             uint      `gorm:"uniqueIndex:idx_notif_acc_user;not null" json:"user_id"`
	SelectedEmailFlags string    `gorm:"type:text" json:"selected_email_flags"`   // JSON array e.g. ["conversation_assignment", "sla_breach"]
	SelectedPushFlags  string    `gorm:"type:text" json:"selected_push_flags"`    // JSON array
	SelectedInAppFlags string    `gorm:"type:text" json:"selected_in_app_flags"`  // JSON array
	Muted              bool      `gorm:"default:false" json:"muted"`
	QuietHoursEnabled  bool      `gorm:"default:false" json:"quiet_hours_enabled"`
	QuietHoursStart    string    `gorm:"size:10;default:'22:00'" json:"quiet_hours_start"`
	QuietHoursEnd      string    `gorm:"size:10;default:'08:00'" json:"quiet_hours_end"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ----------------- RPT / CSAT 满意度问卷 -----------------

// CSATSurvey records customer satisfaction ratings on resolved conversations
type CSATSurvey struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	AccountID       uint       `gorm:"index;not null" json:"account_id"`
	ConversationID  uint       `gorm:"index;not null" json:"conversation_id"`
	Rating          int        `gorm:"not null" json:"rating"` // 1 - 5
	FeedbackText    string     `gorm:"type:text" json:"feedback_text"`
	AssignedAgentID *uint      `gorm:"index" json:"assigned_agent_id"`
	ReviewStatus    string     `gorm:"size:50;default:'pending';index" json:"review_status"` // pending, approved, rejected, flagged
	ReviewerID      *uint      `gorm:"index" json:"reviewer_id,omitempty"`
	ReviewNotes     string     `gorm:"type:text" json:"review_notes,omitempty"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`

	Conversation  *Conversation `gorm:"foreignKey:ConversationID" json:"conversation,omitempty"`
	AssignedAgent *User         `gorm:"foreignKey:AssignedAgentID" json:"assigned_agent,omitempty"`
	Reviewer      *User         `gorm:"foreignKey:ReviewerID" json:"reviewer,omitempty"`
}

// ----------------- OPEN 开放平台 -----------------

// PlatformApp represents an authorized integration on the platform control plane
type PlatformApp struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:255;not null" json:"name"`
	Token     string    `gorm:"size:255;uniqueIndex;not null" json:"token"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ----------------- MOB 移动推送设备订阅 -----------------

// NotificationSubscription holds client push device tokens (FCM, APNs, Browser)
type NotificationSubscription struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	UserID           uint      `gorm:"index;not null" json:"user_id"`
	AccountID        uint      `gorm:"index;not null" json:"account_id"`
	SubscriptionType string    `gorm:"size:50;not null" json:"subscription_type"` // fcm, apns, browser
	PushToken        string    `gorm:"size:512;not null" json:"push_token"`
	DeviceName       string    `gorm:"size:255" json:"device_name"`
	AppVersion       string    `gorm:"size:50" json:"app_version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ----------------- CRM 企业组织与公司 -----------------

// Company represents a customer organization or corporate account
type Company struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	AccountID     uint      `gorm:"index;not null" json:"account_id"`
	Name          string    `gorm:"size:255;not null" json:"name"`
	Domain        string    `gorm:"size:255" json:"domain"`
	Industry      string    `gorm:"size:100" json:"industry"`
	Description   string    `gorm:"type:text" json:"description"`
	ContactsCount int64     `gorm:"-" json:"contacts_count,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CompanyNote represents an internal operator note attached to a company profile
type CompanyNote struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	AccountID uint      `gorm:"index;not null" json:"account_id"`
	CompanyID uint      `gorm:"index;not null" json:"company_id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	User    *User    `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Company *Company `gorm:"foreignKey:CompanyID" json:"company,omitempty"`
}

// ----------------- OPS 营销与触达活动 -----------------

// Campaign defines targeted one-off or recurring outbound message campaigns
type Campaign struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	AccountID       uint       `gorm:"index;not null" json:"account_id"`
	InboxID         uint       `gorm:"index;not null" json:"inbox_id"`
	Title           string     `gorm:"size:255;not null" json:"title"`
	Description     string     `gorm:"type:text" json:"description"`
	Message         string     `gorm:"type:text;not null" json:"message"`
	CampaignType    string     `gorm:"size:50;default:'one_off'" json:"campaign_type"` // one_off, ongoing
	Status          string     `gorm:"size:50;default:'draft'" json:"status"`         // draft, scheduled, active, paused, completed, cancelled
	ScheduledAt     *time.Time `json:"scheduled_at"`
	Audience        string     `gorm:"type:text" json:"audience"`                     // JSON filter
	TriggerRules    string     `gorm:"type:text" json:"trigger_rules,omitempty"`       // URL/metadata/event filter for ongoing
	SenderID        *uint      `json:"sender_id,omitempty"`
	DeliveriesCount int64      `gorm:"-" json:"deliveries_count,omitempty"`
	SentCount       int64      `gorm:"-" json:"sent_count,omitempty"`
	DeliveredCount  int64      `gorm:"-" json:"delivered_count,omitempty"`
	FailedCount     int64      `gorm:"-" json:"failed_count,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ----------------- RPT 服务水平协议 (SLA) -----------------

// SLAPolicy defines response and resolution time targets for customer conversations
type SLAPolicy struct {
	ID                          uint      `gorm:"primaryKey" json:"id"`
	AccountID                   uint      `gorm:"index;not null" json:"account_id"`
	Name                        string    `gorm:"size:255;not null" json:"name"`
	Description                 string    `gorm:"type:text" json:"description"`
	FirstResponseTimeThreshold int       `gorm:"default:3600" json:"first_response_time_threshold"` // in seconds
	NextResponseTimeThreshold  int       `gorm:"default:0" json:"next_response_time_threshold"`     // in seconds
	ResolutionTimeThreshold    int       `gorm:"default:86400" json:"resolution_time_threshold"`   // in seconds
	OnlyDuringBusinessHours     bool      `gorm:"default:true" json:"only_during_business_hours"`
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

// AppliedSLA status constants
const (
	AppliedSLAStatusActive           = "active"
	AppliedSLAStatusHit              = "hit"
	AppliedSLAStatusMissed           = "missed"
	AppliedSLAStatusActiveWithMisses = "active_with_misses"

	SLAEventTypeFRT = "frt"
	SLAEventTypeNRT = "nrt"
	SLAEventTypeRT  = "rt"
)

// AppliedSLA tracks the application and compliance status of an SLA policy on a conversation
type AppliedSLA struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	ConversationID uint      `gorm:"index;not null" json:"conversation_id"`
	SLAPolicyID    uint      `gorm:"index;not null" json:"sla_policy_id"`
	SLAStatus      string    `gorm:"size:50;default:'active';index" json:"sla_status"` // active, hit, missed, active_with_misses
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	Account      *Account      `gorm:"foreignKey:AccountID" json:"account,omitempty"`
	Conversation *Conversation `gorm:"foreignKey:ConversationID" json:"conversation,omitempty"`
	SLAPolicy    *SLAPolicy    `gorm:"foreignKey:SLAPolicyID" json:"sla_policy,omitempty"`
	SLAEvents    []SLAEvent    `gorm:"foreignKey:AppliedSLAID" json:"sla_events,omitempty"`
}

// SLAEvent records a specific SLA breach or threshold event
type SLAEvent struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AppliedSLAID   uint      `gorm:"index;not null" json:"applied_sla_id"`
	ConversationID uint      `gorm:"index;not null" json:"conversation_id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	SLAPolicyID    uint      `gorm:"index;not null" json:"sla_policy_id"`
	InboxID        uint      `gorm:"index;not null" json:"inbox_id"`
	EventType      string    `gorm:"size:50;not null;index" json:"event_type"` // frt, nrt, rt
	Meta           string    `gorm:"type:text" json:"meta"`                     // e.g. {"message_id": 123}
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	AppliedSLA   *AppliedSLA   `gorm:"foreignKey:AppliedSLAID" json:"applied_sla,omitempty"`
	Conversation *Conversation `gorm:"foreignKey:ConversationID" json:"conversation,omitempty"`
	SLAPolicy    *SLAPolicy    `gorm:"foreignKey:SLAPolicyID" json:"sla_policy,omitempty"`
	Inbox        *Inbox        `gorm:"foreignKey:InboxID" json:"inbox,omitempty"`
}

// ----------------- EXT 机器人代理 (AgentBot) -----------------

// AgentBot represents an automated webhook or external service that can participate in conversations
type AgentBot struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccountID   uint      `gorm:"index;not null" json:"account_id"`
	Name        string    `gorm:"size:255;not null" json:"name"`
	Description string    `gorm:"type:text" json:"description"`
	OutgoingURL string    `gorm:"size:1024;not null" json:"outgoing_url"`
	BotType     string    `gorm:"size:50;default:'webhook'" json:"bot_type"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ----------------- CONV 消息多媒体附件 -----------------

// Attachment represents a file or image attached to a message
type Attachment struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	AccountID uint      `gorm:"index;not null" json:"account_id"`
	MessageID uint      `gorm:"index;not null" json:"message_id"`
	FileType  string    `gorm:"size:100" json:"file_type"`
	DataURL   string    `gorm:"type:text;not null" json:"data_url"`
	FileSize  int64     `json:"file_size"`
	CreatedAt time.Time `json:"created_at"`
}

// ----------------- OPS 自定义筛选器与草稿 -----------------

// CustomFilter saves user search and filter criteria
type CustomFilter struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	AccountID  uint      `gorm:"index;not null" json:"account_id"`
	UserID     uint      `gorm:"index;not null" json:"user_id"`
	Name       string    `gorm:"size:255;not null" json:"name"`
	FilterType string    `gorm:"size:50;not null" json:"filter_type"` // conversation, contact
	Query      string    `gorm:"type:text;not null" json:"query"`      // JSON filter query
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// DraftMessage holds an in-progress uncommitted message draft for a conversation
type DraftMessage struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	ConversationID uint      `gorm:"uniqueIndex:idx_conv_user;not null" json:"conversation_id"`
	UserID         uint      `gorm:"uniqueIndex:idx_conv_user;not null" json:"user_id"`
	Message        string    `gorm:"type:text;not null" json:"message"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ConversationParticipant represents an agent participating/collaborating in a conversation
type ConversationParticipant struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ConversationID uint      `gorm:"uniqueIndex:idx_conv_part;not null" json:"conversation_id"`
	UserID         uint      `gorm:"uniqueIndex:idx_conv_part;not null" json:"user_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// ----------------- CHAN 语音通话与会议 (Calls & Conference) -----------------

// Call represents a voice or video call interaction
type Call struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	ContactID      uint      `gorm:"index;not null" json:"contact_id"`
	InboxID        uint      `gorm:"index;not null" json:"inbox_id"`
	ConversationID *uint     `gorm:"index" json:"conversation_id"`
	AgentID        *uint     `gorm:"index" json:"agent_id"`
	Status         string    `gorm:"size:50;default:'initiated'" json:"status"` // initiated, ringing, in_progress, completed, failed
	Direction      string    `gorm:"size:50;default:'outbound'" json:"direction"` // inbound, outbound
	Duration       int       `gorm:"default:0" json:"duration"`                   // in seconds
	RecordingURL   string    `gorm:"size:1024" json:"recording_url"`
	SDPOffer       string    `gorm:"type:text" json:"sdp_offer,omitempty"`
	SDPAnswer      string    `gorm:"type:text" json:"sdp_answer,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Conference represents a room signaling session
type Conference struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	InboxID        uint      `gorm:"index;not null" json:"inbox_id"`
	CallSID        string    `gorm:"size:255;not null" json:"call_sid"`
	ConversationID *uint     `gorm:"index" json:"conversation_id"`
	Token          string    `gorm:"size:255;not null" json:"token"`
	Status         string    `gorm:"size:50;default:'active'" json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ----------------- AUTH & ENT 企业安全、SSO 与会话 -----------------

// SAMLSetting stores enterprise SAML SSO identity provider configuration
type SAMLSetting struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	AccountID    uint      `gorm:"uniqueIndex;not null" json:"account_id"`
	SSOURL       string    `gorm:"size:1024;not null" json:"sso_url"`
	Certificate  string    `gorm:"type:text;not null" json:"certificate"`
	RoleMappings string    `gorm:"type:text" json:"role_mappings"` // JSON mapping SAML groups to roles
	Enabled      bool      `gorm:"default:true" json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserSession tracks active logins and devices
type UserSession struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	UserID         uint      `gorm:"index;not null" json:"user_id"`
	DeviceName     string    `gorm:"size:255;default:'Unknown Device'" json:"device_name"`
	IPAddress      string    `gorm:"size:100;default:'127.0.0.1'" json:"ip_address"`
	LastActivityAt time.Time `gorm:"index" json:"last_activity_at"`
	CreatedAt      time.Time `json:"created_at"`
}

// PasswordResetToken manages forgotten password verification
type PasswordResetToken struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	Email     string    `gorm:"size:255;not null" json:"email"`
	Token     string    `gorm:"size:255;uniqueIndex;not null" json:"token"`
	ExpiresAt time.Time `gorm:"index;not null" json:"expires_at"`
	Used      bool      `gorm:"default:false" json:"used"`
	CreatedAt time.Time `json:"created_at"`
}

// MFAProfile tracks two-factor authentication TOTP setup
type MFAProfile struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	UserID      uint      `gorm:"uniqueIndex;not null" json:"user_id"`
	Secret      string    `gorm:"size:255;not null" json:"secret"`
	Enabled     bool      `gorm:"default:false" json:"enabled"`
	BackupCodes string    `gorm:"type:text" json:"backup_codes"` // JSON string array
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ----------------- ROUTE & MIG 数据导入与迁移 -----------------

// DataImport manages asynchronous batch import jobs
type DataImport struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	AccountID        uint      `gorm:"index;not null" json:"account_id"`
	SourceProvider   string    `gorm:"size:100;default:'csv'" json:"source_provider"` // csv, json
	ImportType       string    `gorm:"size:100;not null" json:"import_type"`           // contacts, conversations
	Status           string    `gorm:"size:50;default:'pending'" json:"status"`       // staged, validated, processing, completed, failed, cancelled, discarded
	TotalRecords     int       `gorm:"default:0" json:"total_records"`
	ProcessedRecords int       `gorm:"default:0" json:"processed_records"`
	FailedRecords    int       `gorm:"default:0" json:"failed_records"`
	SkippedRecords   int       `gorm:"default:0" json:"skipped_records"`
	RawData          string    `gorm:"type:longtext" json:"raw_data,omitempty"`
	ValidationJSON   string    `gorm:"type:text" json:"validation_json,omitempty"`
	ErrorsJSON       string    `gorm:"type:text" json:"errors_json,omitempty"`
	SkippedJSON      string    `gorm:"type:text" json:"skipped_json,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// MigrationJob manages legacy webchat data migration and reconciliation
type MigrationJob struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	AccountID     uint      `gorm:"index;not null" json:"account_id"`
	JobType       string    `gorm:"size:100;not null" json:"job_type"` // webchat_legacy, customer_data
	Status        string    `gorm:"size:50;default:'running'" json:"status"`
	SourceSystem  string    `gorm:"size:100;default:'legacy_webchat'" json:"source_system"`
	SyncedRecords int       `gorm:"default:0" json:"synced_records"`
	Logs          string    `gorm:"type:text" json:"logs"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ----------------- ENT & RPT 企业配额、计费与自定义指标 -----------------

// ReportingEvent stores custom metrics and operational telemetry
type ReportingEvent struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	Name           string    `gorm:"size:255;index;not null" json:"name"`
	Value          float64   `gorm:"default:1.0" json:"value"`
	EventStartTime time.Time `json:"event_start_time"`
	EventEndTime   time.Time `json:"event_end_time"`
	MetadataJSON   string    `gorm:"type:text" json:"metadata_json"`
	CreatedAt      time.Time `json:"created_at"`
}

// AccountLimit manages enterprise capacity and feature quotas
type AccountLimit struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	AccountID         uint      `gorm:"uniqueIndex;not null" json:"account_id"`
	ConversationLimit int       `gorm:"default:10000" json:"conversation_limit"`
	AgentLimit        int       `gorm:"default:50" json:"agent_limit"`
	InboxLimit        int       `gorm:"default:20" json:"inbox_limit"`
	Credits           float64   `gorm:"default:1000.0" json:"credits"`
	Currency          string    `gorm:"size:10;default:'USD'" json:"currency"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// AccountBilling logs financial transactions and usage debits
type AccountBilling struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccountID   uint      `gorm:"index;not null" json:"account_id"`
	ActionType  string    `gorm:"size:50;not null" json:"action_type"` // deposit, deduction
	Amount      float64   `json:"amount"`
	Currency    string    `gorm:"size:10;default:'USD'" json:"currency"`
	Description string    `gorm:"size:255" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// Onboarding records setup wizard progress
type Onboarding struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	AccountID uint      `gorm:"uniqueIndex;not null" json:"account_id"`
	Step      string    `gorm:"size:100;default:'welcome'" json:"step"`
	Industry  string    `gorm:"size:100" json:"industry"`
	Timezone  string    `gorm:"size:100;default:'UTC'" json:"timezone"`
	Completed bool      `gorm:"default:false" json:"completed"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BrandedEmailLayout configures white-label email templates
type BrandedEmailLayout struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccountID   uint      `gorm:"uniqueIndex;not null" json:"account_id"`
	LayoutHTML  string    `gorm:"type:text;not null" json:"layout_html"`
	HeaderColor string    `gorm:"size:50;default:'#1f93ff'" json:"header_color"`
	LogoURL     string    `gorm:"size:1024" json:"logo_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// EmailChannelMigration manages platform email channel cutover
type EmailChannelMigration struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	Provider       string    `gorm:"size:100;not null" json:"provider"` // sendgrid, mailgun, smtp
	Status         string    `gorm:"size:50;default:'completed'" json:"status"`
	ProviderConfig string    `gorm:"type:text" json:"provider_config"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ----------------- AI / Captain / Copilot -----------------

// CaptainAssistant defines an AI Copilot / Captain assistant bot
type CaptainAssistant struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	AccountID          uint      `gorm:"index;not null" json:"account_id"`
	Name               string    `gorm:"size:255;not null" json:"name"`
	Description        string    `gorm:"type:text" json:"description"`
	SystemPrompt       string    `gorm:"type:text" json:"system_prompt"`
	Model              string    `gorm:"size:100;default:'local-heuristic'" json:"model"`
	Status             string    `gorm:"size:50;default:'active'" json:"status"`
	Config             string    `gorm:"type:text" json:"config"`
	ResponseGuidelines string    `gorm:"type:text" json:"response_guidelines"`
	Guardrails         string    `gorm:"type:text" json:"guardrails"`
	Inboxes            []Inbox   `gorm:"many2many:captain_inboxes;" json:"inboxes,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// CaptainInbox defines the binding relationship between CaptainAssistant and Inbox
type CaptainInbox struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	AccountID          uint      `gorm:"index;not null" json:"account_id"`
	CaptainAssistantID uint      `gorm:"index;not null" json:"captain_assistant_id"`
	InboxID            uint      `gorm:"index;not null" json:"inbox_id"`
	Inbox              *Inbox    `gorm:"foreignKey:InboxID" json:"inbox,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// CaptainAssistantResponse defines FAQ / Q&A pairs for Captain & Copilot
type CaptainAssistantResponse struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccountID   uint      `gorm:"index;not null" json:"account_id"`
	AssistantID uint      `gorm:"index;not null" json:"assistant_id"`
	DocumentID  *uint     `gorm:"index" json:"document_id,omitempty"`
	Question    string    `gorm:"size:500;not null" json:"question"`
	Answer      string    `gorm:"type:text;not null" json:"answer"`
	Status      string    `gorm:"size:50;default:'active'" json:"status"` // active, inactive, draft
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CaptainKnowledgeDoc defines knowledge base documents for Captain & Copilot
type CaptainKnowledgeDoc struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	AccountID  uint      `gorm:"index;not null" json:"account_id"`
	Title      string    `gorm:"size:255;not null" json:"title"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	Category   string    `gorm:"size:100" json:"category"`
	Status     string    `gorm:"size:50;default:'ready'" json:"status"` // pending, processing, ready, failed
	ChunkCount int       `gorm:"default:0" json:"chunk_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CaptainDocChunk defines text chunking segments and keyword indices
type CaptainDocChunk struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	AccountID  uint      `gorm:"index;not null" json:"account_id"`
	DocID      uint      `gorm:"index;not null" json:"doc_id"`
	ChunkIndex int       `json:"chunk_index"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	Keywords   string    `gorm:"size:500" json:"keywords"`
	CreatedAt  time.Time `json:"created_at"`
}

// AIScenario defines an AI assistant execution scenario and allowed tools
type AIScenario struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	AccountID    uint      `gorm:"index;not null" json:"account_id"`
	Name         string    `gorm:"size:100;not null" json:"name"`
	Description  string    `gorm:"type:text" json:"description"`
	SystemPrompt string    `gorm:"type:text;not null" json:"system_prompt"`
	AllowedTools string    `gorm:"type:text" json:"allowed_tools"` // JSON array e.g. ["lookup_order", "search_faq", "transfer_agent"]
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AIUsageQuota tracks monthly AI token and request usage per account
type AIUsageQuota struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	AccountID           uint      `gorm:"uniqueIndex;not null" json:"account_id"`
	MonthlyTokenLimit   int64     `gorm:"default:100000" json:"monthly_token_limit"`
	UsedTokens          int64     `gorm:"default:0" json:"used_tokens"`
	MonthlyRequestLimit int       `gorm:"default:1000" json:"monthly_request_limit"`
	UsedRequests        int       `gorm:"default:0" json:"used_requests"`
	ResetAt             time.Time `json:"reset_at"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// Copilot thread & message status and role constants
const (
	CopilotThreadStatusActive   = "active"
	CopilotThreadStatusArchived = "archived"
	CopilotThreadStatusPinned   = "pinned"

	CopilotRoleUser      = "user"
	CopilotRoleAssistant = "assistant"
	CopilotRoleSystem    = "system"

	CopilotFeedbackNone       = "none"
	CopilotFeedbackThumbsUp   = "thumbs_up"
	CopilotFeedbackThumbsDown = "thumbs_down"
)

// CopilotThread represents a multi-turn AI assistant conversation thread
type CopilotThread struct {
	ID             uint                   `gorm:"primaryKey" json:"id"`
	AccountID      uint                   `gorm:"index;not null" json:"account_id"`
	UserID         uint                   `gorm:"index;not null" json:"user_id"`
	ConversationID *uint                  `gorm:"index" json:"conversation_id,omitempty"`
	AssistantID    *uint                  `gorm:"index" json:"assistant_id,omitempty"`
	Title          string                 `gorm:"size:255;not null" json:"title"`
	Status         string                 `gorm:"size:50;default:'active';index" json:"status"` // active, archived, pinned
	Context        string                 `gorm:"type:text" json:"context,omitempty"`
	Metadata       string                 `gorm:"type:text" json:"metadata,omitempty"`
	MessageCount   int                    `gorm:"default:0" json:"message_count"`
	TotalTokens    int                    `gorm:"default:0" json:"total_tokens"`
	LastMessageAt  *time.Time             `gorm:"index" json:"last_message_at,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`

	Account      *Account               `gorm:"foreignKey:AccountID" json:"account,omitempty"`
	User         *User                  `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Conversation *Conversation          `gorm:"foreignKey:ConversationID" json:"conversation,omitempty"`
	Assistant    *CaptainAssistant      `gorm:"foreignKey:AssistantID" json:"assistant,omitempty"`
	Messages     []CopilotThreadMessage `gorm:"foreignKey:ThreadID" json:"messages,omitempty"`
}

// CopilotThreadMessage represents a single message in a CopilotThread
type CopilotThreadMessage struct {
	ID               uint           `gorm:"primaryKey" json:"id"`
	AccountID        uint           `gorm:"index;not null" json:"account_id"`
	ThreadID         uint           `gorm:"index;not null" json:"thread_id"`
	Role             string         `gorm:"size:50;not null;index" json:"role"` // user, assistant, system
	Content          string         `gorm:"type:text;not null" json:"content"`
	Citations        string         `gorm:"type:text" json:"citations,omitempty"`
	SuggestedActions string         `gorm:"type:text" json:"suggested_actions,omitempty"`
	TokenCount       int            `gorm:"default:0" json:"token_count"`
	Feedback         string         `gorm:"size:50;default:'none'" json:"feedback"` // none, thumbs_up, thumbs_down
	FeedbackNotes    string         `gorm:"type:text" json:"feedback_notes,omitempty"`
	Metadata         string         `gorm:"type:text" json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`

	Thread *CopilotThread `gorm:"foreignKey:ThreadID" json:"thread,omitempty"`
}

// CustomRole defines custom agent role and granular permissions
type CustomRole struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccountID   uint      `gorm:"index;not null" json:"account_id"`
	Name        string    `gorm:"size:100;not null" json:"name"`
	Description string    `gorm:"size:255" json:"description"`
	Permissions string    `gorm:"type:text;not null" json:"permissions"` // JSON array
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CallICECandidate stores WebRTC ICE candidate signaling packets
type CallICECandidate struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CallID    uint      `gorm:"index;not null" json:"call_id"`
	Candidate string    `gorm:"type:text;not null" json:"candidate"`
	SDPMid    string    `gorm:"size:100" json:"sdp_mid"`
	SDPMLine  int       `json:"sdp_m_line_index"`
	CreatedAt time.Time `json:"created_at"`
}

// SubscriptionPlan defines SaaS pricing tiers and feature entitlements
type SubscriptionPlan struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Name         string    `gorm:"size:100;not null" json:"name"`
	Slug         string    `gorm:"size:100;uniqueIndex;not null" json:"slug"` // free, starter, pro, enterprise
	PriceCents   int       `gorm:"default:0" json:"price_cents"`
	AgentLimit   int       `gorm:"default:5" json:"agent_limit"`
	AITokenLimit int64     `gorm:"default:100000" json:"ai_token_limit"`
	Features     string    `gorm:"type:text" json:"features"` // JSON array of enabled features
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AccountSubscription tracks active subscription and billing status of a tenant
type AccountSubscription struct {
	ID                 uint              `gorm:"primaryKey" json:"id"`
	AccountID          uint              `gorm:"uniqueIndex;not null" json:"account_id"`
	PlanID             uint              `gorm:"index;not null" json:"plan_id"`
	Status             string            `gorm:"size:50;default:'active'" json:"status"` // active, past_due, canceled
	CurrentPeriodStart time.Time         `json:"current_period_start"`
	CurrentPeriodEnd   time.Time         `json:"current_period_end"`
	CancelAtPeriodEnd  bool              `gorm:"default:false" json:"cancel_at_period_end"`
	Plan               *SubscriptionPlan `gorm:"foreignKey:PlanID" json:"plan,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// WorkingHourConfig item for daily business hours schedule
type WorkingHourConfig struct {
	DayOfWeek    int  `json:"day_of_week"` // 0=Sunday, 1=Monday, ..., 6=Saturday
	OpenHour     int  `json:"open_hour"`
	OpenMinute   int  `json:"open_minute"`
	OpenMinutes  int  `json:"open_minutes,omitempty"`
	CloseHour    int  `json:"close_hour"`
	CloseMinute  int  `json:"close_minute"`
	CloseMinutes int  `json:"close_minutes,omitempty"`
	Closed       bool `json:"closed"`
	ClosedAllDay bool `json:"closed_all_day,omitempty"`
	OpenAllDay   bool `json:"open_all_day,omitempty"`
}

// GetOpenMinute returns the open minute regardless of JSON field naming
func (w *WorkingHourConfig) GetOpenMinute() int {
	if w.OpenMinutes > 0 {
		return w.OpenMinutes
	}
	return w.OpenMinute
}

// GetCloseMinute returns the close minute regardless of JSON field naming
func (w *WorkingHourConfig) GetCloseMinute() int {
	if w.CloseMinutes > 0 {
		return w.CloseMinutes
	}
	return w.CloseMinute
}

// IsClosed returns true if the day is configured as closed
func (w *WorkingHourConfig) IsClosed() bool {
	return w.Closed || w.ClosedAllDay
}

// IsOpenAllDay returns true if the day is configured as open 24 hours
func (w *WorkingHourConfig) IsOpenAllDay() bool {
	if w.OpenAllDay {
		return true
	}
	if w.OpenHour == 0 && w.GetOpenMinute() == 0 {
		if w.CloseHour == 24 || (w.CloseHour == 23 && w.GetCloseMinute() == 59) {
			return true
		}
	}
	return false
}

