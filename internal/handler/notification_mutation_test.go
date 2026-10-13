package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const notificationFaultMarker = "private-notification-storage-diagnostic"

type notificationMutationFixture struct {
	db                            *gorm.DB
	r                             *gin.Engine
	fault                         string
	updates, deletes, reads, hits int
}

func newNotificationMutationFixture(t *testing.T, open func(*testing.T) *gorm.DB) *notificationMutationFixture {
	t.Helper()
	f := &notificationMutationFixture{db: open(t)}
	require.NoError(t, f.db.AutoMigrate(&domain.Notification{}))
	for _, foreign := range []string{"users", "accounts", "conversations", "messages", "notification_settings"} {
		require.False(t, f.db.Migrator().HasTable(foreign), foreign)
	}
	gin.SetMode(gin.TestMode)
	f.r = gin.New()
	f.r.Use(func(c *gin.Context) { c.Set("user_id", uint(7)); c.Next() })
	h := NewMacroNotificationHandler(nil, repository.NewNotificationRepository(f.db), nil)
	g := f.r.Group("/accounts/:account_id/notifications")
	g.PATCH("/:id", h.UpdateNotification)
	g.POST("/:id/read", h.MarkNotificationRead)
	g.POST("/:id/unread", h.MarkNotificationUnread)
	g.POST("/:id/snooze", h.SnoozeNotification)
	g.POST("/:id/unsnooze", h.UnsnoozeNotification)
	g.DELETE("/:id", h.DeleteNotification)
	g.POST("/read_all", h.MarkAllNotificationsRead)
	g.POST("/batch_destroy", h.BatchDeleteNotifications)
	g.DELETE("", h.BatchDeleteNotifications)
	require.NoError(t, f.db.Callback().Update().After("gorm:update").Before("gorm:commit_or_rollback_transaction").Register("notification_mutation_update", func(tx *gorm.DB) {
		if tx.Statement.Table != "notifications" {
			return
		}
		f.updates++
		if f.fault == "update" {
			f.hits++
			_ = tx.AddError(errors.New(notificationFaultMarker))
		}
	}))
	require.NoError(t, f.db.Callback().Delete().After("gorm:delete").Before("gorm:commit_or_rollback_transaction").Register("notification_mutation_delete", func(tx *gorm.DB) {
		if tx.Statement.Table != "notifications" {
			return
		}
		f.deletes++
		if f.fault == "delete" {
			f.hits++
			_ = tx.AddError(errors.New(notificationFaultMarker))
		}
	}))
	require.NoError(t, f.db.Callback().Query().After("gorm:after_query").Register("notification_mutation_read", func(tx *gorm.DB) {
		if tx.Statement.Table != "notifications" {
			return
		}
		f.reads++
		if f.fault == "readback" || f.fault == "readback_missing" {
			f.hits++
			err := errors.New(notificationFaultMarker)
			if f.fault == "readback_missing" {
				err = gorm.ErrRecordNotFound
			}
			_ = tx.AddError(err)
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, f.db.Callback().Update().Remove("notification_mutation_update"))
		require.NoError(t, f.db.Callback().Delete().Remove("notification_mutation_delete"))
		require.NoError(t, f.db.Callback().Query().Remove("notification_mutation_read"))
	})
	return f
}
func (f *notificationMutationFixture) seed(t *testing.T, account, user uint, read, snoozed bool) domain.Notification {
	t.Helper()
	stamp := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	n := domain.Notification{AccountID: account, UserID: user, NotificationType: domain.NotificationTypeSystemAlert, CreatedAt: stamp, UpdatedAt: stamp}
	if read {
		n.ReadAt = &stamp
	}
	if snoozed {
		value := stamp.Add(5 * time.Hour)
		n.SnoozedUntil = &value
	}
	require.NoError(t, f.db.Create(&n).Error)
	return n
}
func (f *notificationMutationFixture) request(method, path, body, fault string) *httptest.ResponseRecorder {
	f.fault, f.updates, f.deletes, f.reads, f.hits = fault, 0, 0, 0, 0
	defer func() { f.fault = "" }()
	req := httptest.NewRequest(method, "/accounts/11/notifications"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	f.r.ServeHTTP(out, req)
	return out
}
func (f *notificationMutationFixture) rows(t *testing.T) []domain.Notification {
	t.Helper()
	var rows []domain.Notification
	require.NoError(t, f.db.Order("id").Find(&rows).Error)
	return rows
}
func assertNotificationMutationFailure(t *testing.T, out *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, 500, out.Code, out.Body.String())
	require.JSONEq(t, `{"success":false,"error":"Notification storage unavailable"}`, out.Body.String())
	require.NotContains(t, out.Body.String(), notificationFaultMarker)
}
func notificationMutationResponse(t *testing.T, out *httptest.ResponseRecorder) domain.Notification {
	t.Helper()
	require.Equal(t, 200, out.Code, out.Body.String())
	var body struct {
		Success bool                 `json:"success"`
		Data    *domain.Notification `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.NotNil(t, body.Data)
	require.NotZero(t, body.Data.ID)
	return *body.Data
}

func requireNotificationTimeEqual(t *testing.T, expected, actual *time.Time) {
	t.Helper()
	if expected == nil {
		require.Nil(t, actual)
		return
	}
	require.NotNil(t, actual)
	require.True(t, expected.Equal(*actual), "expected %v, got %v", expected, actual)
}

func TestNotificationMutationOwners(t *testing.T) {
	runNotificationMutationOwners(t, func(t *testing.T) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ntf.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		conn, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		return db
	})
}

func runNotificationMutationOwners(t *testing.T, open func(*testing.T) *gorm.DB) {
	type operation struct{ name, method, suffix, body string }
	singles := []operation{
		{"read", "POST", "/read", ""}, {"unread", "POST", "/unread", ""},
		{"snooze", "POST", "/snooze", `{"duration":"3h"}`}, {"unsnooze", "POST", "/unsnooze", ""},
		{"patch_read", "PATCH", "", `{"read":true}`}, {"patch_unread", "PATCH", "", `{"read":false}`},
		{"patch_combined", "PATCH", "", `{"read":false,"snoozed_until":"2035-01-02T03:04:05Z"}`},
		{"patch_read_at", "PATCH", "", `{"read_at":"2001-01-02T03:04:05Z"}`},
	}
	t.Run("storage_failures", func(t *testing.T) {
		for _, op := range singles {
			t.Run(op.name, func(t *testing.T) {
				for _, fault := range []string{"update", "readback", "readback_missing"} {
					t.Run(fault, func(t *testing.T) {
						f := newNotificationMutationFixture(t, open)
						n := f.seed(t, 11, 7, true, true)
						f.seed(t, 11, 8, false, false)
						f.seed(t, 12, 7, false, false)
						before := f.rows(t)
						path := fmt.Sprintf("/%d%s", n.ID, op.suffix)
						out := f.request(op.method, path, op.body, fault)
						assertNotificationMutationFailure(t, out)
						require.Equal(t, 1, f.hits)
						require.Equal(t, 1, f.updates, "combined PATCH must use a single update")
						if strings.HasPrefix(fault, "readback") {
							require.Equal(t, 1, f.reads)
						}
						require.Equal(t, before, f.rows(t), "failure must roll back locally executed SQL")
						got := notificationMutationResponse(t, f.request(op.method, path, op.body, ""))
						require.Equal(t, n.ID, got.ID)
						var stored domain.Notification
						require.NoError(t, f.db.First(&stored, n.ID).Error)
						requireNotificationTimeEqual(t, stored.ReadAt, got.ReadAt)
						requireNotificationTimeEqual(t, stored.SnoozedUntil, got.SnoozedUntil)
						switch op.name {
						case "read", "patch_read", "patch_read_at":
							require.NotNil(t, got.ReadAt)
						case "unread", "patch_unread", "patch_combined":
							require.Nil(t, got.ReadAt)
						case "unsnooze":
							require.Nil(t, got.SnoozedUntil)
						}
						if op.name == "patch_combined" {
							require.NotNil(t, got.SnoozedUntil)
							require.Equal(t, "2035-01-02T03:04:05Z", got.SnoozedUntil.UTC().Format(time.RFC3339))
						}
						require.Equal(t, before[1:], f.rows(t)[1:])
					})
				}
			})
		}
		for _, op := range []operation{{"read_all", "POST", "/read_all", ""}, {"delete", "DELETE", "/1", ""}, {"batch_post", "POST", "/batch_destroy", ""}, {"batch_delete", "DELETE", "", ""}} {
			t.Run(op.name, func(t *testing.T) {
				f := newNotificationMutationFixture(t, open)
				n := f.seed(t, 11, 7, false, false)
				f.seed(t, 11, 8, false, false)
				f.seed(t, 12, 7, false, false)
				before := f.rows(t)
				fault := "delete"
				if op.name == "read_all" {
					fault = "update"
				}
				path := op.suffix
				if op.name == "delete" {
					path = fmt.Sprintf("/%d", n.ID)
				}
				out := f.request(op.method, path, op.body, fault)
				assertNotificationMutationFailure(t, out)
				require.Equal(t, 1, f.hits)
				require.Equal(t, before, f.rows(t))
				out = f.request(op.method, path, op.body, "")
				require.Equal(t, 200, out.Code, out.Body.String())
			})
		}
	})
	t.Run("noop_readback_failure", func(t *testing.T) {
		f := newNotificationMutationFixture(t, open)
		n := f.seed(t, 11, 7, true, true)
		before := f.rows(t)
		out := f.request("PATCH", fmt.Sprintf("/%d", n.ID), `{}`, "readback")
		assertNotificationMutationFailure(t, out)
		require.Zero(t, f.updates)
		require.Equal(t, 1, f.hits)
		require.Equal(t, before, f.rows(t))
	})
	t.Run("read_all_scoped", func(t *testing.T) {
		f := newNotificationMutationFixture(t, open)
		f.seed(t, 11, 7, false, false)
		f.seed(t, 11, 7, true, true)
		f.seed(t, 11, 8, false, false)
		f.seed(t, 12, 7, false, false)
		before := f.rows(t)
		out := f.request("POST", "/read_all", "", "")
		require.Equal(t, 200, out.Code, out.Body.String())
		require.JSONEq(t, `{"success":true,"data":{"status":"ok"}}`, out.Body.String())
		after := f.rows(t)
		require.NotNil(t, after[0].ReadAt)
		require.Equal(t, before[1:], after[1:])
	})

	t.Run("scoped_missing", func(t *testing.T) {
		ops := append(append([]operation{}, singles...), operation{"delete", "DELETE", "", ""}, operation{"patch_noop", "PATCH", "", `{}`})
		for _, op := range ops {
			t.Run(op.name, func(t *testing.T) {
				for _, target := range []string{"missing", "deleted", "other_user", "other_account"} {
					t.Run(target, func(t *testing.T) {
						f := newNotificationMutationFixture(t, open)
						f.seed(t, 11, 7, false, false)
						otherUser := f.seed(t, 11, 8, true, true)
						otherAccount := f.seed(t, 12, 7, true, true)
						id := uint(999999)
						switch target {
						case "other_user":
							id = otherUser.ID
						case "other_account":
							id = otherAccount.ID
						case "deleted":
							n := f.seed(t, 11, 7, false, false)
							id = n.ID
							require.NoError(t, f.db.Delete(&n).Error)
						}
						before := f.rows(t)
						out := f.request(op.method, fmt.Sprintf("/%d%s", id, op.suffix), op.body, "")
						require.Equal(t, 404, out.Code, out.Body.String())
						require.JSONEq(t, `{"success":false,"error":"Notification not found"}`, out.Body.String())
						require.Equal(t, before, f.rows(t), "missing updates must not insert/resurrect")
					})
				}
			})
		}
	})
	t.Run("malformed_before_write", func(t *testing.T) {
		for _, route := range []operation{{"snooze", "POST", "/1/snooze", ""}, {"patch", "PATCH", "/1", ""}, {"batch_post", "POST", "/batch_destroy", ""}, {"batch_delete", "DELETE", "", ""}} {
			t.Run(route.name, func(t *testing.T) {
				bodies := []struct{ name, body string }{{"truncated", `{"`}, {"array", `[]`}, {"scalar", `true`}, {"trailing_garbage", `{} !`}, {"trailing_object", `{} {}`}}
				switch route.name {
				case "snooze":
					bodies = append(bodies, struct{ name, body string }{"duration_type", `{"duration":30}`}, struct{ name, body string }{"invalid_time", `{"snoozed_until":"tomorrow"}`}, struct{ name, body string }{"time_type", `{"snoozed_until":10}`})
				case "patch":
					bodies = append(bodies, struct{ name, body string }{"read_type", `{"read":"false"}`}, struct{ name, body string }{"invalid_time", `{"read":true,"snoozed_until":"tomorrow"}`}, struct{ name, body string }{"read_at_type", `{"read_at":false}`}, struct{ name, body string }{"empty_body", ``})
				default:
					bodies = append(bodies, struct{ name, body string }{"only_read_type", `{"only_read":"true"}`}, struct{ name, body string }{"ids_type", `{"ids":"1"}`}, struct{ name, body string }{"partial_ids", `{"ids":[1,"bad"],"only_read":false}`}, struct{ name, body string }{"negative_id", `{"ids":[-1]}`})
				}
				for _, body := range bodies {
					t.Run(body.name, func(t *testing.T) {
						f := newNotificationMutationFixture(t, open)
						n := f.seed(t, 11, 7, false, false)
						f.seed(t, 11, 7, true, true)
						before := f.rows(t)
						path := route.suffix
						if route.name == "snooze" {
							path = fmt.Sprintf("/%d/snooze", n.ID)
						}
						if route.name == "patch" {
							path = fmt.Sprintf("/%d", n.ID)
						}
						out := f.request(route.method, path, body.body, "")
						require.Equal(t, 400, out.Code, out.Body.String())
						require.Zero(t, f.updates)
						require.Zero(t, f.deletes)
						require.Zero(t, f.reads)
						require.Equal(t, before, f.rows(t))
					})
				}
			})
		}
	})
	t.Run("patch_compatibility", func(t *testing.T) {
		for _, tc := range []struct {
			name, body            string
			read, snoozed, writes bool
		}{
			{"read_false_precedes_read_at", `{"read":false,"read_at":"2001-01-01T00:00:00Z"}`, false, true, true},
			{"read_at_marks_now", `{"read_at":"2001-01-01T00:00:00Z"}`, true, true, true},
			{"combined", `{"read":false,"snoozed_until":"2035-01-02T03:04:05Z"}`, false, true, true},
			{"null_fields_unchanged", `{"read":null,"read_at":null,"snoozed_until":null}`, true, true, false},
			{"empty_object", `{}`, true, true, false}, {"null_body", `null`, true, true, false}, {"unknown_field", `{"future":true}`, true, true, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newNotificationMutationFixture(t, open)
				n := f.seed(t, 11, 7, true, true)
				before := f.rows(t)
				got := notificationMutationResponse(t, f.request("PATCH", fmt.Sprintf("/%d", n.ID), tc.body, ""))
				require.Equal(t, tc.read, got.ReadAt != nil)
				require.Equal(t, tc.snoozed, got.SnoozedUntil != nil)
				want := 0
				if tc.writes {
					want = 1
				}
				require.Equal(t, want, f.updates)
				if !tc.writes {
					require.Equal(t, before, f.rows(t))
				}
				if tc.name == "read_at_marks_now" {
					require.WithinDuration(t, time.Now().UTC(), *got.ReadAt, 5*time.Second)
				}
				if tc.name == "combined" {
					require.Equal(t, "2035-01-02T03:04:05Z", got.SnoozedUntil.UTC().Format(time.RFC3339))
				}
			})
		}
	})
	t.Run("snooze_compatibility", func(t *testing.T) {
		for _, tc := range []struct {
			name, body string
			duration   time.Duration
		}{
			{"empty_body", "", time.Hour}, {"whitespace", " \n", time.Hour}, {"empty_object", `{}`, time.Hour}, {"null", `null`, time.Hour},
			{"unknown_duration", `{"duration":"unknown"}`, time.Hour}, {"20m", `{"duration":"20m"}`, 20 * time.Minute}, {"20min", `{"duration":"20min"}`, 20 * time.Minute}, {"20_minutes", `{"duration":"20_minutes"}`, 20 * time.Minute},
			{"3h", `{"duration":"3h"}`, 3 * time.Hour}, {"3_hours", `{"duration":"3_hours"}`, 3 * time.Hour}, {"tomorrow", `{"duration":"tomorrow"}`, 24 * time.Hour}, {"24h", `{"duration":"24h"}`, 24 * time.Hour}, {"1d", `{"duration":"1d"}`, 24 * time.Hour},
			{"case_whitespace", `{"duration":" 3H "}`, 3 * time.Hour}, {"past_time_fallback", `{"snoozed_until":"2001-01-01T00:00:00Z","duration":"3h"}`, 3 * time.Hour}, {"future_time_precedence", `{"snoozed_until":"2035-01-02T03:04:05Z","duration":"20m"}`, 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newNotificationMutationFixture(t, open)
				n := f.seed(t, 11, 7, false, false)
				start := time.Now().UTC()
				got := notificationMutationResponse(t, f.request("POST", fmt.Sprintf("/%d/snooze", n.ID), tc.body, ""))
				require.NotNil(t, got.SnoozedUntil)
				require.Nil(t, got.ReadAt)
				if tc.duration == 0 {
					require.Equal(t, "2035-01-02T03:04:05Z", got.SnoozedUntil.UTC().Format(time.RFC3339))
				} else {
					require.WithinDuration(t, start.Add(tc.duration), *got.SnoozedUntil, 5*time.Second)
				}
			})
		}
	})
	t.Run("batch_compatibility", func(t *testing.T) {
		for _, route := range []operation{{"post", "POST", "/batch_destroy", ""}, {"delete", "DELETE", "", ""}} {
			t.Run(route.name, func(t *testing.T) {
				for _, mode := range []string{"ids", "only_read", "intersection", "empty_ids", "empty_body", "whitespace", "empty_object", "null", "missing_ids"} {
					t.Run(mode, func(t *testing.T) {
						f := newNotificationMutationFixture(t, open)
						read := f.seed(t, 11, 7, true, false)
						unread := f.seed(t, 11, 7, false, false)
						extra := f.seed(t, 11, 7, true, false)
						foreign := f.seed(t, 11, 8, true, false)
						f.seed(t, 12, 7, true, false)
						before := f.rows(t)
						body := ""
						want := 3
						switch mode {
						case "ids":
							body = fmt.Sprintf(`{"ids":[%d,%d,%d,%d]}`, read.ID, read.ID, foreign.ID, 99999)
							want = 1
						case "only_read":
							body = `{"only_read":true}`
							want = 2
						case "intersection":
							body = fmt.Sprintf(`{"ids":[%d,%d,%d],"only_read":true}`, read.ID, unread.ID, foreign.ID)
							want = 1
						case "empty_ids":
							body = `{"ids":[]}`
						case "whitespace":
							body = " \n"
						case "empty_object":
							body = `{}`
						case "null":
							body = `null`
						case "missing_ids":
							body = `{"ids":[999999]}`
							want = 0
						}
						out := f.request(route.method, route.suffix, body, "")
						require.Equal(t, 200, out.Code, out.Body.String())
						require.JSONEq(t, fmt.Sprintf(`{"success":true,"data":{"deleted_count":%d,"status":"ok"}}`, want), out.Body.String())
						rows := f.rows(t)
						require.Len(t, rows, 5-want)
						require.Equal(t, before[3:], rows[len(rows)-2:])
						if mode == "intersection" {
							require.Equal(t, unread.ID, rows[0].ID)
							require.Equal(t, extra.ID, rows[1].ID)
						}
					})
				}
			})
		}
	})
	t.Run("unavailable_table", func(t *testing.T) {
		f := newNotificationMutationFixture(t, open)
		n := f.seed(t, 11, 7, false, false)
		before := f.rows(t)
		require.NoError(t, f.db.Migrator().RenameTable("notifications", "notifications_unavailable"))
		for _, op := range []operation{{"patch", "PATCH", fmt.Sprintf("/%d", n.ID), `{"read":true}`}, {"noop", "PATCH", fmt.Sprintf("/%d", n.ID), `{}`}, {"delete", "DELETE", fmt.Sprintf("/%d", n.ID), ""}, {"batch", "POST", "/batch_destroy", ""}, {"read_all", "POST", "/read_all", ""}} {
			t.Run(op.name, func(t *testing.T) { assertNotificationMutationFailure(t, f.request(op.method, op.suffix, op.body, "")) })
		}
		require.NoError(t, f.db.Migrator().RenameTable("notifications_unavailable", "notifications"))
		require.Equal(t, before, f.rows(t))
	})
}
