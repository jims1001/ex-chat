package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportCountSeriesHTTP(t *testing.T) {
	cfg := reportCountHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "count.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runReportCountSeriesHTTP(t, cfg, db)
}
func reportCountHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "report-count-series-test-secret-12345678", JWTExpirationHours: 24}
}

func runReportCountSeriesHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	for _, id := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: id, Name: fmt.Sprintf("Count account %d", id)}).Error)
	}
	tokens := map[string]string{}
	users := map[string]uint{}
	for _, kind := range []string{"admin", "agent", "report_view", "report_manage", "foreign"} {
		u := domain.User{Name: kind, Email: kind + "@count.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&u).Error)
		users[kind] = u.ID
		m := domain.AccountUser{AccountID: 91, UserID: u.ID, Role: domain.RoleAgent}
		switch kind {
		case "admin":
			m.Role = domain.RoleAdministrator
		case "foreign":
			m.Role = domain.RoleAdministrator
			m.AccountID = 92
		case "report_view", "report_manage":
			role := domain.CustomRole{AccountID: 91, Name: kind, Permissions: `["` + kind + `"]`}
			require.NoError(t, db.Create(&role).Error)
			m.CustomRoleID = &role.ID
		}
		require.NoError(t, db.Create(&m).Error)
		token, err := auth.GenerateToken(&u, cfg.JWTSecret, 24)
		require.NoError(t, err)
		tokens[kind] = token
	}
	day := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	for _, a := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Inbox{ID: a, AccountID: a, Name: "Count inbox", Timezone: "UTC", WebsiteToken: fmt.Sprint("count-", a), WorkingHoursEnabled: true, WorkingHours: `[{"day_of_week":4,"closed":true}]`}).Error)
		require.NoError(t, db.Create(&domain.Contact{ID: a, AccountID: a, Name: "Count contact"}).Error)
	}
	for i, at := range []time.Time{day.AddDate(0, 0, -5), day, day.Add(12 * time.Hour), day.AddDate(0, 0, 2)} {
		id := uint(i + 101)
		require.NoError(t, db.Create(&domain.Conversation{ID: id, DisplayID: id, AccountID: 91, InboxID: 91, ContactID: 91, CreatedAt: at, Status: domain.ConversationStatusOpen}).Error)
	}
	require.NoError(t, db.Create(&domain.Conversation{ID: 900, DisplayID: 900, AccountID: 92, InboxID: 92, ContactID: 92, CreatedAt: day}).Error)
	for i, row := range []struct {
		kind, sender, status string
		private              bool
		at                   time.Time
	}{
		{"incoming", "Contact", "sent", false, day}, {"incoming", "Contact", "sent", false, day.Add(12 * time.Hour)},
		{"outgoing", "User", "sent", false, day}, {"outgoing", "User", "sent", true, day.Add(time.Hour)},
		{"outgoing", "AgentBot", "failed", false, day.Add(2 * time.Hour)}, {"outgoing", "User", "queued", false, day.Add(3 * time.Hour)},
		{"activity", "User", "sent", false, day.Add(4 * time.Hour)}, {"template", "User", "sent", false, day.Add(5 * time.Hour)},
		{"outgoing", "User", "sent", false, day.AddDate(0, 0, 2)}, {"incoming", "Contact", "sent", false, day.AddDate(0, 0, 2)},
	} {
		require.NoError(t, db.Create(&domain.Message{ID: uint(i + 201), AccountID: 91, ConversationID: 101, MessageType: row.kind, SenderType: row.sender, SenderID: users["admin"], Content: "private count fixture", Private: row.private, Status: row.status, CreatedAt: row.at}).Error)
	}
	require.NoError(t, db.Create(&domain.Message{AccountID: 92, ConversationID: 900, MessageType: "outgoing", SenderType: "User", SenderID: users["foreign"], Content: "foreign private", CreatedAt: day}).Error)
	query := func(metric string) url.Values {
		return url.Values{"metric": {metric}, "since": {"2024-02-29"}, "until": {"2024-03-02"}}
	}
	request := func(version, path, principal string, q url.Values, ctx context.Context) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/"+version+"/accounts/91/reports"+path+"?"+q.Encode(), nil)
		if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		req.Header.Set("X-Request-Id", "count-request")
		req.Header.Set("X-Correlation-Id", "count-correlation")
		if ctx != nil {
			req = req.WithContext(ctx)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	checkError := func(w *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		require.Equal(t, status, w.Code, w.Body.String())
		var e struct {
			Error middleware.ReportCountAPIError `json:"error"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
		require.Equal(t, code, e.Error.Code)
		require.NotEmpty(t, e.Error.Category)
		require.NotEmpty(t, e.Error.Message)
		require.NotNil(t, e.Error.FieldErrors)
		require.Equal(t, "count-request", e.Error.RequestID)
		require.Equal(t, "count-correlation", e.Error.CorrelationID)
		require.NotContains(t, w.Body.String(), "private")
	}
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {

			t.Run("fixed_offset_calendar", func(t *testing.T) {
				for _, offset := range []struct {
					text    string
					seconds int
				}{
					{"5.5", 19800}, {"5.75", 20700}, {"-3.5", -12600}, {"25", 90000}, {"0.0025", 9},
				} {
					for _, grouping := range []string{"hour", "day", "month", "year"} {
						for _, metric := range []string{"conversations_count", "incoming_messages_count", "outgoing_messages_count"} {
							t.Run(offset.text+"/"+grouping+"/"+metric, func(t *testing.T) {
								instants := []time.Time{day, day.Add(12 * time.Hour), day.AddDate(0, 0, 2)}
								if metric == "outgoing_messages_count" {
									instants = []time.Time{day, day.Add(time.Hour), day.Add(2 * time.Hour), day.Add(3 * time.Hour), day.AddDate(0, 0, 2)}
								}
								counts := map[int64]int64{}
								for _, instant := range instants {
									civil := instant.In(time.FixedZone("test fixed offset", offset.seconds))
									year, month, date := civil.Date()
									hour := 0
									switch grouping {
									case "hour":
										hour = civil.Hour()
									case "month":
										date = 1
									case "year":
										month = time.January
										date = 1
									}
									label := time.Date(year, month, date, hour, 0, 0, 0, civil.Location()).Unix()
									counts[label]++
								}
								want := make([]domain.ReportCountPoint, 0, len(counts))
								for label, count := range counts {
									want = append(want, domain.ReportCountPoint{Value: count, Timestamp: label})
								}
								sort.Slice(want, func(i, j int) bool { return want[i].Timestamp < want[j].Timestamp })
								q := query(metric)
								q.Set("group_by", grouping)
								q.Set("timezone_offset", offset.text)
								w := request(version, "", "admin", q, nil)
								require.Equal(t, 200, w.Code, w.Body.String())
								expected, err := json.Marshal(want)
								require.NoError(t, err)
								require.JSONEq(t, string(expected), w.Body.String())
							})
						}
					}
				}
			})
			t.Run("authority_and_sparse_counts", func(t *testing.T) {
				for _, principal := range []string{"admin", "report_view", "report_manage"} {
					for _, metric := range []string{"conversations_count", "incoming_messages_count", "outgoing_messages_count"} {
						q := query(metric)
						w := request(version, "", principal, q, nil)
						require.Equal(t, 200, w.Code, w.Body.String())
						n := 2
						if metric == "outgoing_messages_count" {
							n = 4
						}
						require.JSONEq(t, fmt.Sprintf(`[{"value":%d,"timestamp":%d},{"value":1,"timestamp":%d}]`, n, day.Unix(), day.AddDate(0, 0, 2).Unix()), w.Body.String())
					}
				}
				checkError(request(version, "", "", query("conversations_count"), nil), 401, "unauthenticated")
				checkError(request(version, "", "agent", query("conversations_count"), nil), 403, "forbidden")
				checkError(request(version, "", "foreign", query("conversations_count"), nil), 403, "forbidden")
				req := httptest.NewRequest("GET", "/api/"+version+"/accounts/999/reports?metric=conversations_count&since=2024-02-29&until=2024-02-29", nil)
				req.Header.Set("Authorization", "Bearer "+tokens["admin"])
				req.Header.Set("X-Request-Id", "count-request")
				req.Header.Set("X-Correlation-Id", "count-correlation")
				missing := httptest.NewRecorder()
				r.ServeHTTP(missing, req)
				checkError(missing, 404, "not_found")

			})
			t.Run("date_exact_aliases_defaults_business_hours", func(t *testing.T) {
				for _, q := range []url.Values{
					{"metric": {"outgoing_messages_count"}, "since": {"2024-02-29"}, "until": {"2024-02-29"}},
					{"metric": {"outgoing_messages_count"}, "since": {"2024-02-29T05:45:00+05:45"}, "until": {"2024-03-01T00:00:00Z"}},
					{"metric": {"outgoing_messages_count"}, "from": {"2024-02-29"}, "to": {"2024-02-29"}},
					{"metric": {"outgoing_messages_count"}, "since": {"2024-02-29"}, "until": {"2024-02-29"}, "from": {"bad"}, "to": {"bad"}, "type": {"account"}, "group_by": {"invalid_falls_to_day"}, "timezone_offset": {"0"}, "business_hours": {"true"}, "format": {"json"}},
				} {
					w := request(version, "", "admin", q, nil)
					require.Equal(t, 200, w.Code, w.Body.String())
					require.JSONEq(t, fmt.Sprintf(`[{"value":4,"timestamp":%d}]`, day.Unix()), w.Body.String())
				}
			})
			t.Run("exact_submicrosecond_HTTP_bounds", func(t *testing.T) {
				for _, q := range []url.Values{
					{"metric": {"conversations_count"}, "since": {"2024-02-29T00:00:00.000000001Z"}, "until": {"2024-02-29T12:00:00.000000001Z"}},
					{"metric": {"conversations_count"}, "since": {"1709164800"}, "until": {"1709208000"}},
				} {
					w := request(version, "", "admin", q, nil)
					require.Equal(t, 200, w.Code, w.Body.String())
					require.JSONEq(t, fmt.Sprintf(`[{"value":1,"timestamp":%d}]`, day.Unix()), w.Body.String())
				}
			})
			t.Run("exact_empty_reversed_and_no_events", func(t *testing.T) {
				for _, q := range []url.Values{
					{"metric": {"conversations_count"}, "since": {"2024-02-29T00:00:00Z"}, "until": {"2024-02-29T00:00:00Z"}},
					{"metric": {"conversations_count"}, "since": {"2024-03-02T00:00:00Z"}, "until": {"2024-02-29T00:00:00Z"}},
					{"metric": {"conversations_count"}, "since": {"1969-12-30"}, "until": {"1969-12-30"}},
				} {
					w := request(version, "", "admin", q, nil)
					require.Equal(t, 200, w.Code, w.Body.String())
					require.Equal(t, "[]", w.Body.String())
				}
			})
			t.Run("query_year10000_representation", func(t *testing.T) {
				at := time.Date(9999, 12, 31, 12, 0, 0, 0, time.UTC)
				conv := domain.Conversation{AccountID: 91, InboxID: 91, ContactID: 91, CreatedAt: at, Status: domain.ConversationStatusOpen}
				require.NoError(t, db.Create(&conv).Error)
				defer db.Delete(&conv)
				for _, kind := range []string{"incoming", "outgoing"} {
					msg := domain.Message{AccountID: 91, ConversationID: 101, MessageType: kind, SenderType: "AgentBot", Status: "failed", Private: true, CreatedAt: at}
					require.NoError(t, db.Create(&msg).Error)
					defer db.Delete(&msg)
				}
				for _, grouping := range []string{"hour", "day", "month", "year"} {
					t.Run(grouping, func(t *testing.T) {
						for _, metric := range []string{"conversations_count", "incoming_messages_count", "outgoing_messages_count"} {
							t.Run(metric, func(t *testing.T) {
								q := url.Values{"metric": {metric}, "group_by": {grouping}, "since": {"9999-12-31"}, "until": {"9999-12-31"}}
								w := request(version, "", "admin", q, nil)
								if db.Dialector.Name() == "sqlite" {
									checkError(w, 422, "report_query_bound_unsupported")
									var body struct {
										Error middleware.ReportCountAPIError `json:"error"`
									}
									require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
									require.Equal(t, "validation", body.Error.Category)
									require.False(t, body.Error.Retryable)
									require.Empty(t, body.Error.Dependency)
									require.Equal(t, []middleware.ReportCountFieldError{{Field: "since/until", Code: "report_query_bound_unsupported", Message: "Report query bounds require supported timestamp representations"}}, body.Error.FieldErrors)
								} else {
									require.Equal(t, 200, w.Code, w.Body.String())
								}
								// Nominal year/month/day successor is 10000, but the actual clipped bound is supported.
								q.Set("until", "9999-12-31T23:59:59.999999999Z")
								w = request(version, "", "admin", q, nil)
								require.Equal(t, 200, w.Code, w.Body.String())
								label := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
								switch grouping {
								case "hour":
									label = at
								case "month":
									label = time.Date(9999, 12, 1, 0, 0, 0, 0, time.UTC)
								case "year":
									label = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
								}
								require.JSONEq(t, fmt.Sprintf(`[{"value":1,"timestamp":%d}]`, label.Unix()), w.Body.String())
								for _, bounds := range [][2]string{{"253402300800", "253402300800"}, {"253402300801", "253402300800"}, {"0000-01-01T00:00:00Z", "0000-01-01T00:00:00Z"}} {
									q.Set("since", bounds[0])
									q.Set("until", bounds[1])
									w = request(version, "", "admin", q, nil)
									require.Equal(t, 200, w.Code, w.Body.String())
									require.Equal(t, "[]", w.Body.String())
								}
							})
						}
					})
				}
			})
			t.Run("calendar_groupings", func(t *testing.T) {
				for _, grouping := range []string{"hour", "day", "month", "year"} {
					t.Run(grouping, func(t *testing.T) {
						for _, metric := range []string{"conversations_count", "incoming_messages_count", "outgoing_messages_count"} {
							t.Run(metric, func(t *testing.T) {
								for _, business := range []string{"false", "true"} {
									q := query(metric)
									q.Set("group_by", grouping)
									q.Set("business_hours", business)
									w := request(version, "", "admin", q, nil)
									require.Equal(t, 200, w.Code, w.Body.String())
									var got []domain.ReportCountPoint
									require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
									var expected []domain.ReportCountPoint
									n := int64(2)
									if metric == "outgoing_messages_count" {
										n = 4
									}
									switch grouping {
									case "hour":
										offsets := []int{0, 12, 48}
										if metric == "outgoing_messages_count" {
											offsets = []int{0, 1, 2, 3, 48}
										}
										for _, hour := range offsets {
											expected = append(expected, domain.ReportCountPoint{Value: 1, Timestamp: day.Add(time.Duration(hour) * time.Hour).Unix()})
										}
									case "day":
										expected = []domain.ReportCountPoint{{Value: n, Timestamp: day.Unix()}, {Value: 1, Timestamp: day.AddDate(0, 0, 2).Unix()}}
									case "month":
										expected = []domain.ReportCountPoint{{Value: n, Timestamp: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC).Unix()}, {Value: 1, Timestamp: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC).Unix()}}
									case "year":
										expected = []domain.ReportCountPoint{{Value: n + 1, Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()}}
									}
									require.Equal(t, expected, got)
									// Clip selection to exact fractional endpoints while retaining natural labels.
									q.Set("since", "2024-02-29T00:00:00.000000001Z")
									q.Set("until", "2024-02-29T12:00:00.000000001Z")
									w = request(version, "", "admin", q, nil)
									require.Equal(t, 200, w.Code, w.Body.String())
									require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
									expected = nil
									label := day
									switch grouping {
									case "hour":
										label = day.Add(12 * time.Hour)
									case "month":
										label = time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
									case "year":
										label = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
									}
									if metric == "outgoing_messages_count" {
										if grouping == "hour" {
											for _, hour := range []int{1, 2, 3} {
												expected = append(expected, domain.ReportCountPoint{Value: 1, Timestamp: day.Add(time.Duration(hour) * time.Hour).Unix()})
											}
										} else {
											expected = []domain.ReportCountPoint{{Value: 3, Timestamp: label.Unix()}}
										}
									} else {
										expected = []domain.ReportCountPoint{{Value: 1, Timestamp: label.Unix()}}
									}
									require.Equal(t, expected, got)
								}
							})
						}
					})
				}
			})
			t.Run("calendar_selected_bucket_budget", func(t *testing.T) {
				for _, grouping := range []string{"hour", "day", "month", "year"} {
					t.Run(grouping, func(t *testing.T) {
						start := time.Date(1800, 1, 1, 0, 0, 0, 1, time.UTC)
						boundary := time.Date(1800, 1, 1, 0, 0, 0, 0, time.UTC)
						switch grouping {
						case "hour":
							boundary = boundary.Add(400 * time.Hour)
						case "day":
							boundary = boundary.AddDate(0, 0, 400)
						case "month":
							boundary = boundary.AddDate(0, 400, 0)
						case "year":
							boundary = boundary.AddDate(400, 0, 0)
						}
						q := url.Values{"metric": {"conversations_count"}, "group_by": {grouping}, "since": {start.Format(time.RFC3339Nano)}, "until": {boundary.Format(time.RFC3339Nano)}}
						w := request(version, "", "admin", q, nil)
						require.Equal(t, 200, w.Code, w.Body.String())
						q.Set("until", boundary.Add(time.Nanosecond).Format(time.RFC3339Nano))
						checkError(request(version, "", "admin", q, nil), 422, "report_range_too_large")
					})
				}
			})
			t.Run("validation_and_unsupported", func(t *testing.T) {
				for _, tc := range []struct {
					field, value, code string
					status             int
				}{
					{"metric", "", "invalid_metric", 400}, {"metric", "unknown", "invalid_metric", 400}, {"metric", "reply_time", "unsupported_metric", 422}, {"metric", "resolutions_count", "unsupported_metric", 422},
					{"type", "other", "invalid_type", 400}, {"type", "agent", "unsupported_dimension", 422}, {"type", "team", "unsupported_dimension", 422}, {"type", "inbox", "unsupported_dimension", 422}, {"type", "label", "unsupported_dimension", 422},
					{"id", "1", "unsupported_id", 422}, {"id", "bad", "invalid_id", 400}, {"group_by", "week", "unsupported_grouping", 422},
					{"timezone_offset", "5.5000000000000001", "unsupported_timezone_offset", 422}, {"timezone_offset", "NaN", "invalid_timezone_offset", 400}, {"timezone_offset", "bad", "invalid_timezone_offset", 400}, {"timezone", "Asia/Tokyo", "unsupported_timezone", 422},
					{"since", "bad", "invalid_time_bound", 400}, {"until", "bad", "invalid_time_bound", 400}, {"since", " ", "invalid_time_bound", 400}, {"until", "", "invalid_time_bound", 400},
					{"format", "csv", "unsupported_format", 422}, {"format", "other", "invalid_format", 400}, {"business_hours", "maybe", "invalid_business_hours", 400}, {"unknown", "private-value", "unknown_parameter", 400},
				} {
					q := query("conversations_count")
					q.Set(tc.field, tc.value)
					checkError(request(version, "", "admin", q, nil), tc.status, tc.code)
				}
				q := query("conversations_count")
				q.Set("since", "bad")
				q.Set("from", "2024-02-29")
				checkError(request(version, "", "admin", q, nil), 400, "invalid_time_bound")
				q = query("conversations_count")
				q["metric"] = []string{"conversations_count", "incoming_messages_count"}
				checkError(request(version, "", "admin", q, nil), 400, "duplicate_parameter")
			})
			t.Run("budget_400_and_401_days", func(t *testing.T) {
				q := query("conversations_count")
				q.Set("since", day.Format(time.RFC3339))
				q.Set("until", day.AddDate(0, 0, 400).Format(time.RFC3339))
				require.Equal(t, 200, request(version, "", "admin", q, nil).Code)
				q.Set("until", day.AddDate(0, 0, 400).Add(time.Nanosecond).Format(time.RFC3339Nano))
				checkError(request(version, "", "admin", q, nil), 422, "report_range_too_large")
			})
			t.Run("legacy_errors_unchanged", func(t *testing.T) {
				w := request(version, "/summary", "", query("conversations_count"), nil)
				require.Equal(t, 401, w.Code)
				require.JSONEq(t, `{"success":false,"error":"Authorization header is required"}`, w.Body.String())
				w = request(version, "/summary", "agent", query("conversations_count"), nil)
				require.Equal(t, 403, w.Code)
				require.JSONEq(t, `{"success":false,"error":"You do not have permission to perform this action"}`, w.Body.String())
			})
			t.Run("cancel_and_expired_deadline_fail_closed", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				checkError(request(version, "", "admin", query("conversations_count"), ctx), 503, "request_cancelled")
				ctx, end := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer end()
				checkError(request(version, "", "admin", query("conversations_count"), ctx), 504, "report_timeout")
			})
		})
	}
	t.Run("current_demotion_and_revocation", func(t *testing.T) {
		require.NoError(t, db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", 91, users["admin"]).Update("role", domain.RoleAgent).Error)
		checkError(request("v2", "", "admin", query("conversations_count"), nil), 403, "forbidden")
		require.NoError(t, db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", 91, users["admin"]).Update("role", domain.RoleAdministrator).Error)
		token := domain.RevokedToken{Token: tokens["admin"], UserID: users["admin"], ExpiresAt: time.Now().Add(time.Hour)}
		require.NoError(t, db.Create(&token).Error)
		checkError(request("v1", "", "admin", query("conversations_count"), nil), 401, "unauthenticated")
		require.NoError(t, db.Delete(&token).Error)
	})
	t.Run("storage_representation_contract", func(t *testing.T) {
		if db.Dialector.Name() == "sqlite" {
			require.NoError(t, db.Model(&domain.Conversation{}).Where("id = ?", 101).Update("created_at", "2024-02-24T00:00:00Z").Error)
			for _, version := range []string{"v1", "v2"} {
				w := request(version, "", "admin", query("conversations_count"), nil)
				checkError(w, 503, "report_storage_representation_unsupported")
				require.Contains(t, w.Body.String(), `"retryable":false`)
				require.NotContains(t, w.Body.String(), "2024-02-24")
			}
			// Other owner's independent message count remains available.
			require.Equal(t, 200, request("v2", "", "admin", query("outgoing_messages_count"), nil).Code)
			require.NoError(t, db.Model(&domain.Conversation{}).Where("id = ?", 101).Update("created_at", day.AddDate(0, 0, -5)).Error)
		} else {
			// PostgreSQL normalizes timestamptz storage and remains independent of the
			// conservative SQLite text guard, even when input has a numeric offset.
			require.NoError(t, db.Model(&domain.Conversation{}).Where("id = ?", 101).Update("created_at", day.AddDate(0, 0, -5).In(time.FixedZone("offset", 20700))).Error)
			require.Equal(t, 200, request("v2", "", "admin", query("conversations_count"), nil).Code)
		}
	})
	if db.Dialector.Name() == "postgres" {
		t.Run("inflight_auth_deadline", func(t *testing.T) {
			blocker := db.Begin()
			require.NoError(t, blocker.Error)
			t.Cleanup(func() { _ = blocker.Rollback().Error })
			require.NoError(t, blocker.Exec("LOCK TABLE revoked_tokens IN ACCESS EXCLUSIVE MODE").Error)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			started := time.Now()
			checkError(request("v2", "", "admin", query("conversations_count"), ctx), 504, "report_timeout")
			require.Less(t, time.Since(started), 2*time.Second)
			require.NoError(t, blocker.Rollback().Error)
		})
	}
	t.Run("dependency_errors_are_private", func(t *testing.T) {
		var logs bytes.Buffer
		logger.SetOutput(&logs, slog.LevelDebug, "json")
		defer logger.SetOutput(os.Stdout, slog.LevelInfo, "json")
		require.NoError(t, db.Migrator().DropTable(&domain.Message{}))
		checkError(request("v2", "", "admin", query("outgoing_messages_count"), nil), 503, "report_unavailable")
		require.NoError(t, db.Migrator().DropTable(&domain.RevokedToken{}))
		logs.Reset()
		checkError(request("v1", "", "admin", query("conversations_count"), nil), 500, "internal_error")
		require.NotContains(t, logs.String(), tokens["admin"])
		require.NotContains(t, logs.String(), "SELECT")
		require.NotContains(t, logs.String(), "no such table")
		require.NotContains(t, logs.String(), "does not exist")
	})
}
