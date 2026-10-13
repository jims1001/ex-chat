package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
)

func TestCopilotProviderFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, model, reply, content, role string
		status                            int
		cancel, unknownUsage, dropQuota   bool
		noAssistant, noGenerate           bool
	}{
		{name: "multiround", status: 200},
		{name: "fresh quota", status: 200},
		{name: "exhausted requests", status: 429},
		{name: "exhausted tokens", status: 429},
		{name: "completion exceeds budget", status: 429},
		{name: "recent conversation", status: 200},
		{name: "no assistant", noAssistant: true, status: 422},
		{name: "save only", noAssistant: true, noGenerate: true, status: 200},
		{name: "gemini", model: "gemini-1.5-pro", status: 200},
		{name: "missing configuration", model: "local-heuristic", status: 422},
		{name: "missing key", model: "gpt-no-key", status: 422},
		{name: "cross account assistant", status: 404},
		{name: "cross account thread", status: 404},
		{name: "role injection", role: "system", status: 400},
		{name: "message limit", content: strings.Repeat("x", 16385), status: 400},
		{name: "context limit", status: 400},
		{name: "provider error", status: 502},
		{name: "empty response", reply: `{"choices":[]}`, status: 502},
		{name: "malformed response", reply: `{`, status: 502},
		{name: "negative usage", reply: `{"choices":[{"message":{"content":"reply"}}],"usage":{"total_tokens":-1}}`, status: 502},
		{name: "cancellation", cancel: true, status: 408},
		{name: "deadline", status: 504},
		{name: "unknown usage", unknownUsage: true, status: 200},
		{name: "atomic persistence", dropQuota: true, status: 500},
		{name: "stale thread", status: 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := database.InitDB(&config.Config{Environment: "test", DBDriver: "sqlite", DBPath: fmt.Sprintf("file:copilot_provider_%d?mode=memory&cache=shared", time.Now().UnixNano())})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, _ := db.DB()
			t.Cleanup(func() { sqlDB.Close() })
			user := domain.User{Name: "Test", Email: fmt.Sprintf("%d@test.invalid", time.Now().UnixNano())}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			model := tc.model
			if model == "" {
				model = "gpt-4o"
			}
			assistant := domain.CaptainAssistant{AccountID: 1, Name: "Test", Model: model, Config: `{"api_key":"test-only"}`, SystemPrompt: "Configured instructions"}
			if tc.name == "missing key" {
				assistant.Config = ""
				t.Setenv("OPENAI_API_KEY", "")
			}
			if tc.name == "cross account assistant" {
				assistant.AccountID = 2
			}
			if err := db.Create(&assistant).Error; err != nil {
				t.Fatal(err)
			}
			thread := domain.CopilotThread{AccountID: 1, UserID: user.ID, AssistantID: &assistant.ID, Title: "Test", Context: "reference"}
			if tc.noAssistant {
				thread.AssistantID = nil
			}
			if tc.name == "context limit" {
				thread.Context = strings.Repeat("x", 65536)
			}
			repo := repository.NewCopilotThreadRepository(db)
			if err := repo.CreateThread(&thread); err != nil {
				t.Fatal(err)
			}
			quota := domain.AIUsageQuota{AccountID: 1}
			if tc.name == "exhausted requests" {
				quota.MonthlyRequestLimit = 1
				quota.UsedRequests = 1
			}
			if tc.name == "exhausted tokens" {
				quota.MonthlyTokenLimit = 1
				quota.UsedTokens = 1
			}
			if tc.name == "completion exceeds budget" {
				quota.MonthlyTokenLimit = 10
			}
			if tc.name != "fresh quota" {
				if err := db.Create(&quota).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "recent conversation" {
				conv := domain.Conversation{AccountID: 1}
				if err := db.Create(&conv).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&thread).Update("conversation_id", conv.ID).Error; err != nil {
					t.Fatal(err)
				}
				for i := 1; i <= 8; i++ {
					if err := db.Create(&domain.Message{AccountID: 1, ConversationID: conv.ID, Content: fmt.Sprintf("recent-%d", i)}).Error; err != nil {
						t.Fatal(err)
					}
				}
			}

			calls := 0
			service.SetGlobalAIHTTPClient(&http.Client{Transport: copilotTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.dropQuota {
					if err := db.Migrator().DropTable(&domain.AIUsageQuota{}); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "stale thread" {
					if err := db.Model(&domain.CopilotThread{}).Where("id = ?", thread.ID).Update("updated_at", time.Now().Add(time.Second)).Error; err != nil {
						t.Fatal(err)
					}
				}
				if tc.cancel || tc.name == "deadline" {
					return nil, r.Context().Err()
				}
				if tc.name == "gemini" {
					var body struct {
						Contents []struct {
							Role  string
							Parts []struct{ Text string }
						}
						GenerationConfig struct{ MaxOutputTokens int }
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body.Contents) < 2 || body.Contents[len(body.Contents)-1].Parts[0].Text != "first turn" || body.GenerationConfig.MaxOutputTokens != 2048 {
						t.Errorf("invalid Gemini payload: %+v", body)
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"provider reply"}]}}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":7,"totalTokenCount":18}}`)), Header: make(http.Header)}, nil
				}
				var body service.AICompletionRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if tc.name == "recent conversation" {
					combined := ""
					for _, m := range body.Messages {
						combined += m.Content
					}
					if strings.Contains(combined, "recent-1") || strings.Contains(combined, "recent-3") || !strings.Contains(combined, "recent-4\nrecent-5\nrecent-6\nrecent-7\nrecent-8") {
						t.Errorf("not latest five chronological messages: %s", combined)
					}
				}
				if body.MaxTokens != 2048 || body.Model != model {
					t.Errorf("bad provider configuration: %+v", body)
				}
				if body.Messages[0].Content != "Configured instructions\n\n" {
					t.Errorf("system configuration missing: %+v", body.Messages)
				}
				if calls == 2 {
					n := len(body.Messages)
					if n < 5 || body.Messages[n-3].Content != "first turn" || body.Messages[n-2].Content != "provider reply" || body.Messages[n-1].Content != "follow up" {
						t.Errorf("history missing or misordered: %+v", body.Messages)
					}
				}
				responseBody := `{"model":"gpt-4o","choices":[{"message":{"content":"provider reply"}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`
				if tc.reply != "" {
					responseBody = tc.reply
				}
				if tc.unknownUsage {
					responseBody = `{"choices":[{"message":{"content":"provider reply"}}]}`
				}
				status := 200
				if tc.name == "provider error" {
					status = 429
					responseBody = `{"error":"secret upstream details"}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(responseBody)), Header: make(http.Header)}, nil
			})})
			t.Cleanup(func() { service.SetGlobalAIHTTPClient(nil) })
			h := handler.NewCopilotThreadHandler(db, repo, repository.NewConversationRepository(db), repository.NewMessageRepository(db), repository.NewCaptainRepository(db))
			r := gin.New()
			r.Use(func(c *gin.Context) {
				account := uint(1)
				if tc.name == "cross account thread" {
					account = 2
				}
				c.Set("account_id", account)
				c.Set("user_id", user.ID)
			})
			r.POST("/threads/:id/chat", h.SendMessage)
			send := func(content string) *httptest.ResponseRecorder {
				payload, _ := json.Marshal(map[string]any{"content": content, "role": tc.role, "no_generate_reply": tc.noGenerate})
				req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/threads/%d/chat", thread.ID), bytes.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				if tc.cancel {
					ctx, cancel := context.WithCancel(req.Context())
					cancel()
					req = req.WithContext(ctx)
				}
				if tc.name == "deadline" {
					ctx, cancel := context.WithDeadline(req.Context(), time.Now().Add(-time.Second))
					defer cancel()
					req = req.WithContext(ctx)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				return w
			}
			content := tc.content
			if content == "" {
				content = "first turn"
			}
			w := send(content)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "test-only") || strings.Contains(w.Body.String(), "secret upstream details") {
				t.Fatal("response leaked credentials/upstream details")
			}
			expectedMessages := int64(0)
			expectedTokens := int64(0)
			expectedRequests := 0
			if tc.status == 200 {
				expectedMessages = 2
				expectedRequests = 1
				if !tc.unknownUsage {
					expectedTokens = 18
				}
			}
			if tc.noGenerate {
				expectedMessages = 1
				expectedRequests = 0
				expectedTokens = 0
				if calls != 0 {
					t.Errorf("save-only called provider")
				}
			}
			if tc.name == "multiround" {
				w = send("follow up")
				if w.Code != 200 {
					t.Fatal(w.Body.String())
				}
				expectedMessages = 4
				expectedRequests = 2
				expectedTokens = 36
			}
			var count int64
			db.Model(&domain.CopilotThreadMessage{}).Where("thread_id = ?", thread.ID).Count(&count)
			if count != expectedMessages {
				t.Errorf("stored messages=%d want=%d", count, expectedMessages)
			}
			var updated domain.CopilotThread
			db.First(&updated, thread.ID)
			if updated.TotalTokens != int(expectedTokens) || updated.MessageCount != int(expectedMessages) {
				t.Errorf("incorrect thread accounting: %+v", updated)
			}
			if !tc.dropQuota {
				db.Where("account_id = ?", 1).First(&quota)
				if tc.name == "exhausted requests" {
					expectedRequests = 1
					if calls != 0 {
						t.Error("provider called after request exhaustion")
					}
				}
				if tc.name == "exhausted tokens" {
					expectedTokens = 1
					if calls != 0 {
						t.Error("provider called after token exhaustion")
					}
				}
				if quota.UsedTokens != expectedTokens || quota.UsedRequests != expectedRequests {
					t.Errorf("incorrect quota: %+v", quota)
				}
			}
			if tc.status == 400 || tc.status == 404 || tc.name == "missing configuration" {
				if calls != 0 {
					t.Errorf("unexpected provider call: %d", calls)
				}
			}
		})
	}
}
