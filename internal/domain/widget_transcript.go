package domain

import (
	"context"
	"errors"
)

var ErrWidgetTranscriptForbidden = errors.New("a current visitor session for this transcript is required")

// WidgetTranscriptRequest is formed only after signed visitor-session, live
// inbox/source mapping and exact conversation ownership checks. SourceID comes
// from the signed claims, never a caller-provided source hint or JWT JTI.
type WidgetTranscriptRequest struct {
	AccountID, InboxID, ContactID, ConversationID uint
	SourceID, Key, Email                          string
	InvalidInput                                  bool
}

type WidgetTranscriptPrepared struct {
	InboxSelected     bool
	InboxRoute        *InboxSMTPBinding
	Eligible          bool
	From, To, Subject string
	Render            func([]Message) (string, string)
}

type WidgetTranscriptAdmission struct {
	EmailLogID    uint
	Email         string
	MessagesCount int
}

// The mandatory authorize callback rechecks every supplied token's revocation,
// signed visitor claims, live mapping and conversation before private lookup or
// lost-ack recovery. The generic MSG contract does not authorize visitors.
// Existing shared transcript errors distinguish invalid/conflict/unavailable.
type WidgetTranscriptQueue interface {
	Submit(context.Context, WidgetTranscriptRequest, func(context.Context) error, func(context.Context) (WidgetTranscriptPrepared, error)) (*WidgetTranscriptAdmission, bool, error)
	Status(context.Context, WidgetTranscriptRequest, uint, func(context.Context) error) (map[string]any, error)
}
