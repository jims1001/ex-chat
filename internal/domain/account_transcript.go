package domain

import (
	"context"
	"errors"
)

var (
	ErrAccountTranscriptInvalid     = errors.New("invalid transcript request or Idempotency-Key")
	ErrAccountTranscriptConflict    = errors.New("transcript request conflicts with its accepted idempotency key")
	ErrAccountTranscriptUnavailable = errors.New("transcript queue is unavailable; retry with the same Idempotency-Key")
	ErrAccountTranscriptNotFound    = errors.New("transcript delivery was not found")
)

// The key identifies one explicit send, scoped to account, authenticated user,
// action and conversation. Email is the literal request value; an empty value
// means resolve the contact address once on first admission.
type AccountTranscriptRequest struct {
	AccountID, UserID, ConversationID uint
	Key, Email                        string
	InvalidInput                      bool
}

type AccountTranscriptPrepared struct {
	InboxSelected     bool
	InboxRoute        *InboxSMTPBinding
	Eligible          bool
	From, To, Subject string
	Render            func([]Message) (string, string)
}

type AccountTranscriptAdmission struct {
	EmailLogID    uint
	Email         string
	MessagesCount int
}

// AccountTranscriptQueue is a combined-server owner port. Preparation runs only
// for a new eligible request, under the MSG account lifecycle fence. It must
// reload current conversation/render metadata through the existing owner ports.
type AccountTranscriptQueue interface {
	Submit(context.Context, AccountTranscriptRequest, func(context.Context) (AccountTranscriptPrepared, error)) (*AccountTranscriptAdmission, bool, error)
	Status(context.Context, uint, uint, uint) (map[string]any, error)
}
