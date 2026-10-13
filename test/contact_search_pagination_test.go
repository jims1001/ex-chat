package test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Exercise the actual account route, including authentication and tenant scope.
func TestContactSearchPaginationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "search.db"), JWTSecret: "contact-search-pagination-secret-12345", JWTExpirationHours: 24}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runContactSearchPaginationHTTP(t, cfg, db)
}

// Opt-in uses an isolated schema on an explicitly supplied local database.
func TestPostgresContactSearchPaginationHTTP(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for local PostgreSQL contact search regression")
	}
	c, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	hosts := []string{c.Host}
	for _, fallback := range c.Fallbacks {
		hosts = append(hosts, fallback.Host)
	}
	for _, host := range hosts {
		ip := net.ParseIP(host)
		require.True(t, host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	}
	c.ConnectTimeout = 5 * time.Second
	admin := stdlib.OpenDB(*c)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	schema := "contact_search_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
	c.RuntimeParams["search_path"] = schema
	conn := stdlib.OpenDB(*c)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	cfg := &config.Config{Environment: "test", DBDriver: "postgres", JWTSecret: "contact-search-pagination-secret-12345", JWTExpirationHours: 24}
	runContactSearchPaginationHTTP(t, cfg, db)
}

func runContactSearchPaginationHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	account := domain.Account{Name: "Search account"}
	require.NoError(t, db.Create(&account).Error)
	other := domain.Account{Name: "Other account"}
	require.NoError(t, db.Create(&other).Error)
	user := domain.User{Name: "Search agent", Email: "search@example.test", Role: domain.RoleAgent}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&domain.AccountUser{AccountID: account.ID, UserID: user.ID, Role: domain.RoleAgent}).Error)
	token, err := auth.GenerateToken(&user, cfg.JWTSecret, 24)
	require.NoError(t, err)
	request := func(accountID uint, query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/contacts/search%s", accountID, query), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, query := range []string{"", "?q=", "?q=%20%09%0A", "?search=someone", "?q=&search=someone"} {
		t.Run("required_q/"+query, func(t *testing.T) {
			w := request(account.ID, query)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
		})
	}
	type result struct {
		Payload       []domain.Contact `json:"payload"`
		Data          []domain.Contact `json:"data"`
		HasMore       *bool            `json:"has_more"`
		ContactsCount *int             `json:"contacts_count"`
		Meta          struct {
			Count       int `json:"count"`
			CurrentPage int `json:"current_page"`
			PageSize    int `json:"page_size"`
		} `json:"meta"`
	}
	read := func(t *testing.T, query string, total, page, want int, more bool) result {
		t.Helper()
		w := request(account.ID, query)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var got result
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		require.NotNil(t, got.Payload, "payload must be an array")
		require.NotNil(t, got.Data, "data must be an array")
		require.Len(t, got.Payload, want)
		require.Equal(t, got.Payload, got.Data)
		require.NotNil(t, got.HasMore)
		require.Equal(t, more, *got.HasMore)
		require.NotNil(t, got.ContactsCount)
		require.Equal(t, want, *got.ContactsCount)
		require.Equal(t, total, got.Meta.Count)
		require.Equal(t, page, got.Meta.CurrentPage)
		require.Equal(t, 15, got.Meta.PageSize)
		for _, contact := range got.Payload {
			require.Equal(t, account.ID, contact.AccountID)
		}
		return got
	}
	for _, n := range []int{0, 15, 16, 30, 31} {
		t.Run(fmt.Sprintf("matches_%d", n), func(t *testing.T) {
			query := fmt.Sprintf("needle%dx", n)
			ids := []uint{}
			for i := 0; i < n; i++ {
				c := domain.Contact{AccountID: account.ID, Name: fmt.Sprintf("%s %02d", query, i)}
				require.NoError(t, db.Create(&c).Error)
				ids = append(ids, c.ID)
			}
			// A foreign match must never affect payload, total, or lookahead.
			for i := 0; i < 16; i++ {
				require.NoError(t, db.Create(&domain.Contact{AccountID: other.ID, Name: query}).Error)
			}
			seen := []uint{}
			for page := 1; page <= n/15+2; page++ {
				want := n - (page-1)*15
				if want < 0 {
					want = 0
				}
				if want > 15 {
					want = 15
				}
				got := read(t, fmt.Sprintf("?q=%s&page=%d", query, page), n, page, want, page*15 < n)
				for _, c := range got.Payload {
					seen = append(seen, c.ID)
				}
			}
			expected := []uint{}
			for i := len(ids) - 1; i >= 0; i-- {
				expected = append(expected, ids[i])
			}
			require.Equal(t, expected, seen, "descending pages must neither omit nor duplicate a contact")
			for _, page := range []int{int(^uint(0)>>1)/15 + 2, int(^uint(0) >> 1)} {
				read(t, fmt.Sprintf("?q=%s&page=%d", query, page), n, page, 0, false)
			}
			want := n
			if want > 15 {
				want = 15
			}
			for _, params := range []string{"&page_size=1", "&limit=100", "&page_size=100&limit=1", "&per_page=2", "&page_size=garbage", "&page=0", "&page=-1", "&page=garbage"} {
				read(t, "?q="+query+params, n, 1, want, n > 15)
			}
		})
	}
	t.Run("matching", func(t *testing.T) {
		c := domain.Contact{AccountID: account.ID, Name: "MixedCase Unique", Email: "mailmatch@example.test", PhoneNumber: "+1555019944", Identifier: "idunique", CustomAttributes: `{"tag":"attrunique"}`}
		require.NoError(t, db.Create(&c).Error)
		for _, q := range []string{"  mIxEdCaSe  ", "MAILMATCH", "555019944", "idunique", "attrunique"} {
			got := read(t, "?q="+url.QueryEscape(q), 1, 1, 1, false)
			require.Equal(t, c.ID, got.Payload[0].ID)
		}
	})
	t.Run("foreign_account_forbidden", func(t *testing.T) { require.Equal(t, http.StatusForbidden, request(other.ID, "?q=needle").Code) })
}
