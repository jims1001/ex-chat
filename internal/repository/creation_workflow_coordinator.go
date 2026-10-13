package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// CreationWorkflowCoordinator belongs to ORC/application composition. It owns
// no database and cannot mutate CON or MSG storage except through owner ports.
// CON never calls MSG; only MSG's permitted activity command calls back to CON.
type CreationWorkflowCoordinator struct {
	conversations CreationWorkflowConversationPort
	messages      ConversationCreationMessagePort
}

type ConversationCreationMessagePort interface {
	CreateCreationWorkflowMessage(context.Context, string, int, *domain.Message, int) (*domain.Message, error)
}

type CreationWorkflowConversationPort interface {
	ReadCreationWorkflow(context.Context, uint, uint) (*CreationWorkflowPlan, error)
	PendingCreationWorkflows(context.Context, int) ([]CreationWorkflowRef, error)
	FinalizeCreationWorkflow(context.Context, uint, uint, string, []uint) error
	RejectCreationWorkflow(context.Context, CreationWorkflowRef) error
}

func NewCreationWorkflowCoordinator(conversations CreationWorkflowConversationPort, messages ConversationCreationMessagePort) *CreationWorkflowCoordinator {
	return &CreationWorkflowCoordinator{conversations: conversations, messages: messages}
}

func (c *CreationWorkflowCoordinator) CompleteCreationWorkflow(ctx context.Context, accountID, conversationID uint) ([]domain.Message, error) {
	if c == nil || c.conversations == nil {
		return nil, gorm.ErrInvalidData
	}
	plan, err := c.conversations.ReadCreationWorkflow(ctx, accountID, conversationID)
	if err != nil || plan == nil {
		return nil, err
	}
	if c.messages == nil {
		return nil, fmt.Errorf("MSG creation workflow port is unavailable")
	}
	if plan.AccountID != accountID || plan.ConversationID != conversationID {
		return nil, gorm.ErrInvalidData
	}
	messages := make([]domain.Message, 0, len(plan.Messages))
	messageIDs := make([]uint, 0, len(plan.Messages))
	for i, planned := range plan.Messages {
		msg, err := c.messages.CreateCreationWorkflowMessage(ctx, plan.WorkflowID, i, &planned, plan.CreatedVersion+i)
		if err != nil {
			return messages, err
		}
		if msg == nil || msg.ID == 0 || msg.AccountID != accountID || msg.ConversationID != conversationID {
			return messages, gorm.ErrInvalidData
		}
		messages = append(messages, *msg)
		messageIDs = append(messageIDs, msg.ID)
	}
	return messages, c.conversations.FinalizeCreationWorkflow(ctx, accountID, conversationID, plan.WorkflowID, messageIDs)
}

func (c *CreationWorkflowCoordinator) ReconcileCreationWorkflows(ctx context.Context, limit int) (int, error) {
	if c == nil || c.conversations == nil {
		return 0, gorm.ErrInvalidData
	}
	refs, err := c.conversations.PendingCreationWorkflows(ctx, limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	var firstError error
	for _, ref := range refs {
		_, err := c.CompleteCreationWorkflow(ctx, ref.AccountID, ref.ConversationID)
		if err == nil {
			completed++
			continue
		}
		if errors.Is(err, gorm.ErrInvalidData) || errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrCreationWorkflowMessageConflict) {
			if err := c.conversations.RejectCreationWorkflow(ctx, ref); err != nil {
				return completed, err
			}
			completed++
			continue
		}
		if firstError == nil {
			firstError = err
		}
	}
	return completed, firstError
}
