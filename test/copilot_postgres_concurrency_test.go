package test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// PostgreSQL's READ COMMITTED statement snapshots are essential to these races;
// SQLite-only tests cannot establish parent/child row-lock ordering correctness.
func TestCopilotPostgresConcurrency(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for PostgreSQL concurrency regression")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	adminSQL, _ := admin.DB()
	defer adminSQL.Close()
	schema := fmt.Sprintf("copilot_concurrency_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	db, err := database.InitDB(&config.Config{Environment: "test", DBDriver: "postgres", DBDSN: dsn + " search_path=" + schema})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	account := domain.Account{Name: "Copilot concurrency"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	user := domain.User{Name: "Test", Email: "copilot-concurrency@test.invalid"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	assistant := domain.CaptainAssistant{AccountID: account.ID, Name: "Test", Model: "gpt-4o", Config: `{"api_key":"test-only"}`}
	if err := db.Create(&assistant).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewCopilotThreadRepository(db)
	newThread := func() domain.CopilotThread {
		thread := domain.CopilotThread{AccountID: account.ID, UserID: user.ID, AssistantID: &assistant.ID, Title: "Concurrent"}
		if err := repo.CreateThread(&thread); err != nil {
			t.Fatal(err)
		}
		return thread
	}
	t.Run("clear waits for generation then clears committed turns", func(t *testing.T) {
		thread := newThread()
		tx := db.Begin()
		if tx.Error != nil {
			t.Fatal(tx.Error)
		}
		defer tx.Rollback()
		// Same parent-first transaction order as SendMessage's CAS and AddMessage.
		if err := tx.Model(&domain.CopilotThread{}).Where("id = ?", thread.ID).Update("updated_at", time.Now()).Error; err != nil {
			t.Fatal(err)
		}
		generationRepo := repository.NewCopilotThreadRepository(tx)
		for _, role := range []string{"user", "assistant"} {
			if err := generationRepo.AddMessage(&domain.CopilotThreadMessage{AccountID: account.ID, ThreadID: thread.ID, Role: role, Content: "uncommitted generation", TokenCount: 9}); err != nil {
				t.Fatal(err)
			}
		}
		clearDB, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		clearSQL, _ := clearDB.DB()
		defer clearSQL.Close()
		clearDone := make(chan error, 1)
		go func() {
			clearDone <- repository.NewCopilotThreadRepository(clearDB).ClearMessages(account.ID, thread.ID)
		}()
		// Wait until PostgreSQL confirms the clear connection is blocked on the
		// parent row, rather than relying on goroutine scheduling or a fixed sleep.
		deadline := time.Now().Add(5 * time.Second)
		for {
			var waiting int64
			err := db.Raw("SELECT count(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND wait_event_type = 'Lock' AND query LIKE '%copilot_threads%'").Scan(&waiting).Error
			if err != nil {
				t.Fatal(err)
			}
			if waiting > 0 {
				break
			}
			select {
			case err := <-clearDone:
				t.Fatalf("clear did not wait on parent lock: %v", err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("clear never acquired a waiting parent lock")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := tx.Commit().Error; err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-clearDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("clear deadlocked")
		}
		var count int64
		if err := db.Model(&domain.CopilotThreadMessage{}).Where("thread_id = ?", thread.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(&thread, thread.ID).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 || thread.MessageCount != 0 || thread.TotalTokens != 0 {
			t.Fatalf("clear/generation drift: messages=%d stats=%+v", count, thread)
		}
	})
	t.Run("two threads cannot persist beyond last quota request", func(t *testing.T) {
		first, second := newThread(), newThread()
		quota := domain.AIUsageQuota{AccountID: account.ID, MonthlyRequestLimit: 1, MonthlyTokenLimit: 100}
		if err := db.Create(&quota).Error; err != nil {
			t.Fatal(err)
		}
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		service.SetGlobalAIHTTPClient(&http.Client{Transport: copilotTestTransport(func(r *http.Request) (*http.Response, error) {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"real mock reply"}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))}, nil
		})})
		defer service.SetGlobalAIHTTPClient(nil)
		h := handler.NewCopilotThreadHandler(db, repo, repository.NewConversationRepository(db), repository.NewMessageRepository(db), repository.NewCaptainRepository(db))
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("account_id", account.ID); c.Set("user_id", user.ID) })
		r.POST("/threads/:id/chat", h.SendMessage)
		results := make(chan *httptest.ResponseRecorder, 2)
		for _, id := range []uint{first.ID, second.ID} {
			go func(id uint) {
				req := httptest.NewRequest("POST", fmt.Sprintf("/threads/%d/chat", id), strings.NewReader(`{"content":"concurrent prompt"}`))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				results <- w
			}(id)
		}
		for i := 0; i < 2; i++ {
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("both calls did not reach provider")
			}
		}
		close(release)
		statuses := map[int]int{}
		for i := 0; i < 2; i++ {
			select {
			case w := <-results:
				statuses[w.Code]++
				if w.Code != 200 && w.Code != 429 {
					t.Errorf("unexpected result %d: %s", w.Code, w.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("concurrent generation deadlocked")
			}
		}
		if statuses[200] != 1 || statuses[429] != 1 {
			t.Fatalf("unexpected statuses: %v", statuses)
		}
		if err := db.First(&quota, quota.ID).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		db.Model(&domain.CopilotThreadMessage{}).Where("thread_id IN ?", []uint{first.ID, second.ID}).Count(&count)
		if quota.UsedRequests != 1 || quota.UsedTokens != 18 || count != 2 {
			t.Fatalf("quota overrun or orphan turns: quota=%+v messages=%d", quota, count)
		}
	})
}
