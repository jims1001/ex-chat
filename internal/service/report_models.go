package service

import "time"

// ReportFilter specifies temporal and operational filtering for reports
type ReportFilter struct {
	Since         *time.Time
	Until         *time.Time
	BusinessHours bool
}

// AccountSummaryReport represents aggregated account-level reporting metrics
type AccountSummaryReport struct {
	// Legacy / standard fields (maintained for full backward compatibility)
	TotalConversations    int64 `json:"total_conversations"`
	OpenConversations     int64 `json:"open_conversations"`
	ResolvedConversations int64 `json:"resolved_conversations"`
	PendingConversations  int64 `json:"pending_conversations"`
	TotalMessages         int64 `json:"total_messages"`
	TotalContacts         int64 `json:"total_contacts"`

	// Standard & Advanced V2 metrics
	ConversationsCount         int64   `json:"conversations_count"`
	IncomingMessagesCount      int64   `json:"incoming_messages_count"`
	OutgoingMessagesCount      int64   `json:"outgoing_messages_count"`
	ResolutionsCount           int64   `json:"resolutions_count"`
	ResolutionRate             float64 `json:"resolution_rate"`               // % of total conversations resolved
	AvgFirstResponseTime       float64 `json:"avg_first_response_time"`        // in seconds
	AvgResolutionTime          float64 `json:"avg_resolution_time"`           // in seconds
	AvgReplyTime               float64 `json:"avg_reply_time"`                // in seconds
	FirstContactResolutionRate float64 `json:"first_contact_resolution_rate"` // % resolved with 1 agent reply
	BotResolutionsCount        int64   `json:"bot_resolutions_count"`         // resolved by bot without human agent
	BotHandoffsCount           int64   `json:"bot_handoffs_count"`            // handed off from bot to agent
}

// AgentMetric represents performance metrics for an individual agent
type AgentMetric struct {
	UserID                uint    `json:"user_id"`
	Name                  string  `json:"name"`
	Email                 string  `json:"email"`
	AssignedConversations int64   `json:"assigned_conversations"`
	ResolvedConversations int64   `json:"resolved_conversations"`
	AvgFirstResponseTime  float64 `json:"avg_first_response_time,omitempty"`
	AvgResolutionTime     float64 `json:"avg_resolution_time,omitempty"`
	AvgReplyTime          float64 `json:"avg_reply_time,omitempty"`
	ResolutionRate        float64 `json:"resolution_rate,omitempty"`
	TotalMessagesSent     int64   `json:"total_messages_sent,omitempty"`
}

// TrendPoint represents a single data point in a time series
type TrendPoint struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// ConversationTrendsReport represents volume trends over time
type ConversationTrendsReport struct {
	CurrentPeriodTotal  int64        `json:"current_period_total"`
	PreviousPeriodTotal int64        `json:"previous_period_total"`
	GrowthPercentage    float64      `json:"growth_percentage"`
	Trends              []TrendPoint `json:"trends"`
}

// TeamMetric represents performance metrics for a team
type TeamMetric struct {
	TeamID                uint    `json:"team_id"`
	Name                  string  `json:"name"`
	MemberCount           int     `json:"member_count"`
	AssignedConversations int64   `json:"assigned_conversations"`
	ResolvedConversations int64   `json:"resolved_conversations"`
	AvgFirstResponseTime  float64 `json:"avg_first_response_time,omitempty"`
	AvgResolutionTime     float64 `json:"avg_resolution_time,omitempty"`
	ResolutionRate        float64 `json:"resolution_rate,omitempty"`
}

// InboxMetric represents channel and operational metrics for an inbox
type InboxMetric struct {
	InboxID               uint    `json:"inbox_id"`
	Name                  string  `json:"name"`
	ChannelType           string  `json:"channel_type"`
	TotalConversations    int64   `json:"total_conversations"`
	OpenConversations     int64   `json:"open_conversations"`
	ResolvedConversations int64   `json:"resolved_conversations"`
	AvgFirstResponseTime  float64 `json:"avg_first_response_time,omitempty"`
	AvgResolutionTime     float64 `json:"avg_resolution_time,omitempty"`
	AvgReplyTime          float64 `json:"avg_reply_time,omitempty"`
	ResolutionRate        float64 `json:"resolution_rate,omitempty"`
}

// LabelMetric represents metrics for a conversation label
type LabelMetric struct {
	LabelID               uint    `json:"label_id"`
	Title                 string  `json:"title"`
	Color                 string  `json:"color"`
	ConversationCount     int64   `json:"conversation_count"`
	ResolvedConversations int64   `json:"resolved_conversations,omitempty"`
	AvgFirstResponseTime  float64 `json:"avg_first_response_time,omitempty"`
	AvgResolutionTime     float64 `json:"avg_resolution_time,omitempty"`
}

// FirstResponseDistributionBuckets defines 5 standard time buckets
type FirstResponseDistributionBuckets struct {
	ZeroToOneHour       int64 `json:"0_to_1h"`
	OneToFourHours      int64 `json:"1_to_4h"`
	FourToEightHours    int64 `json:"4_to_8h"`
	EightToTwentyFour   int64 `json:"8_to_24h"`
	TwentyFourHoursPlus int64 `json:"24h_plus"`
}

// ChannelFRTDistribution defines first response distribution per channel
type ChannelFRTDistribution struct {
	ChannelType  string                           `json:"channel_type"`
	Distribution FirstResponseDistributionBuckets `json:"distribution"`
}

// FirstResponseDistributionReport contains distribution metrics
type FirstResponseDistributionReport struct {
	Total    FirstResponseDistributionBuckets `json:"total"`
	Channels []ChannelFRTDistribution         `json:"channels"`
	// Legacy 4 buckets for backward compatibility
	Under15m int64 `json:"under_15m"`
	Under1h  int64 `json:"under_1h"`
	Under4h  int64 `json:"under_4h"`
	Over4h   int64 `json:"over_4h"`
}
