package handler

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestInboundHandlersCompleteDurableReopenBeforeMessageFact(t *testing.T) {
	for _, channel := range []string{"widget", "public"} {
		for _, mode := range []string{"allowed", "muted", "blocked", "legacy"} {
			t.Run(channel+"/"+mode, func(t *testing.T) {
				f := newPublicVisitorFixture(t)
				capture := mode != "legacy"
				f.h.convRepo.SetAutomationCapture(capture)
				f.h.msgRepo.SetAutomationCapture(capture)
				require.NoError(t, f.db.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"status": domain.ConversationStatusResolved, "muted": mode == "muted"}).Error)
				require.NoError(t, f.db.Model(&domain.Contact{}).Where("id = ?", f.owner.ID).Update("blocked", mode == "blocked").Error)
				before, err := f.h.convRepo.FindSummaryByID(f.inbox.AccountID, f.conversation.ID)
				require.NoError(t, err)
				path := f.contactPath() + fmt.Sprintf("/conversations/%d/messages", f.conversation.ID)
				body := `{"content":"Owner workflow inbound message"}`
				if channel == "widget" {
					h := NewConversationHandler(f.h.convRepo, f.h.msgRepo, f.h.inboxRepo, f.h.contactRepo, nil, nil)
					h.SetJWTSecret(publicVisitorTestSecret)
					f.r.POST("/widget/messages", h.WidgetCreateMessage)
					path = "/widget/messages?website_token=" + f.inbox.WebsiteToken
					body = fmt.Sprintf(`{"conversation_id":%d,"content":"Owner workflow inbound message"}`, f.conversation.ID)
				}
				response := f.request(http.MethodPost, path, body, f.session)
				require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
				after, err := f.h.convRepo.FindSummaryByID(f.inbox.AccountID, f.conversation.ID)
				require.NoError(t, err)
				expectedStatus := domain.ConversationStatusOpen
				if mode == "muted" || mode == "blocked" {
					expectedStatus = domain.ConversationStatusResolved
				}
				require.Equal(t, expectedStatus, after.Status)
				require.Equal(t, before.UnreadCount+1, after.UnreadCount)
				facts, err := f.h.msgRepo.PendingAutomationFacts(context.Background(), 10)
				require.NoError(t, err)
				if capture {
					require.Equal(t, before.ObjectVersion+1, after.ObjectVersion, "no post-activity handler mutation may stale the MSG fact")
					require.Len(t, facts, 1)
					require.EqualValues(t, after.ObjectVersion, facts[0].Payload["object_version"])
				} else {
					require.Empty(t, facts)
					require.Equal(t, before.ObjectVersion+2, after.ObjectVersion, "legacy keeps its existing separate activity and status transitions")
				}
			})
		}
	}
}
