package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func confirmationSnapshotDB(t *testing.T, postgres bool, module string) *gorm.DB {
	t.Helper()
	if postgres {
		return postgresModuleDB(t, module)
	}
	return newModuleTestDB(t, module)
}

func confirmationSnapshotRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

func confirmationSnapshotRewrite(t *testing.T, original []byte, path string, edit func(*ModuleSnapshot)) {
	t.Helper()
	var file snapshotFile
	require.NoError(t, json.Unmarshal(original, &file))
	edit(&file.Snapshot)
	canonical, err := json.Marshal(file.Snapshot)
	require.NoError(t, err)
	file.Checksum = sha256Bytes(canonical)
	raw, err := json.Marshal(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
}

func confirmationSnapshotNoSecrets(t *testing.T, raw []byte, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		require.NotEmpty(t, secret)
		require.NotContains(t, string(raw), secret)
	}
}

func confirmationSnapshotNoProof(t *testing.T, user domain.User) {
	t.Helper()
	// Guard the complete binding inventory, including future Confirmation fields.
	value, typ := reflect.ValueOf(user), reflect.TypeOf(user)
	for i := 0; i < typ.NumField(); i++ {
		if strings.HasPrefix(typ.Field(i).Name, "Confirmation") {
			require.Equal(t, "-", typ.Field(i).Tag.Get("json"), typ.Field(i).Name)
			require.True(t, value.Field(i).IsZero(), "%s was restored", typ.Field(i).Name)
		}
	}
}

func confirmationSnapshotRejectLink(t *testing.T, db *gorm.DB, raw string) {
	t.Helper()
	repo := repository.NewChannelAuthEnterpriseRepositoryWithOwnerStores(db, nil, nil, nil, nil, nil, nil)
	user, err := repo.ConfirmUserByToken(raw)
	require.ErrorIs(t, err, repository.ErrConfirmationInvalid)
	require.Nil(t, user, "invalid restored proof returned a session-capable identity")
	h := handler.NewAuthHandler(&config.Config{JWTSecret: "snapshot-auth-test-only", JWTExpirationHours: 1}, repository.NewUserRepository(db), nil)
	h.SetEnterpriseRepo(repo)
	router := gin.New()
	router.GET("/auth/confirmation", h.ConfirmEmail)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/confirmation?confirmation_token="+raw, nil))
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
	confirmationSnapshotNoSecrets(t, response.Body.Bytes(), raw, `"token":`, "mfa_token", "confirmation_token")
	var sessions int64
	require.NoError(t, db.Model(&domain.UserSession{}).Count(&sessions).Error)
	require.Zero(t, sessions)
}

func TestConfirmationOwnerSnapshotNeverRestoresProof(t *testing.T) {
	testConfirmationOwnerSnapshotNeverRestoresProof(t, false)
}

func TestPostgresConfirmationOwnerSnapshotNeverRestoresProof(t *testing.T) {
	testConfirmationOwnerSnapshotNeverRestoresProof(t, true)
}

func testConfirmationOwnerSnapshotNeverRestoresProof(t *testing.T, postgres bool) {
	source, target := confirmationSnapshotDB(t, postgres, "IAM"), confirmationSnapshotDB(t, postgres, "IAM")
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	expires := now.Add(repository.ConfirmationTokenLifetime)
	issued := now.Add(time.Second)
	var users []domain.User
	var tokens, secrets []string
	for i, state := range []string{"pending", "active", "consumed", "legacy_plaintext"} {
		raw := strings.Repeat(fmt.Sprintf("%x", i+10), 64)
		digest, err := repository.ConfirmationTokenDigest(raw)
		require.NoError(t, err)
		user := domain.User{ID: uint(i + 21), Name: state, Email: state + "@example.test", PasswordHash: "owner-password-hash", AuthVersion: 7,
			ConfirmationToken: digest, ConfirmationEmail: state + "@example.test", ConfirmationAuthVersion: 7,
			ConfirmationSentAt: &now, ConfirmationExpiresAt: &expires, CreatedAt: now, UpdatedAt: now}
		if state != "pending" {
			user.ConfirmationIssuedAt = &issued
		}
		if state == "legacy_plaintext" {
			user.ConfirmationToken = raw
		}
		require.NoError(t, source.Create(&user).Error)
		users = append(users, user)
		tokens = append(tokens, raw)
		secrets = append(secrets, raw, digest)
	}
	// A genuine pre-backup consumption is retained as a confirmation fact.
	sourceRepo := repository.NewChannelAuthEnterpriseRepository(source)
	consumed, err := sourceRepo.ConfirmUserByToken(tokens[2])
	require.NoError(t, err)
	require.NotNil(t, consumed.ConfirmedAt)
	require.NoError(t, source.First(&users[2], users[2].ID).Error)
	secrets = append(secrets, "confirmation_token", "confirmation_email", "confirmation_auth_version", "confirmation_sent_at", "confirmation_issued_at", "confirmation_expires_at")
	path := filepath.Join(t.TempDir(), "iam-confirmation.json")
	require.NoError(t, CreateSnapshot(source, "IAM", "confirmation-recovery", path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	original := confirmationSnapshotRead(t, path)
	confirmationSnapshotNoSecrets(t, original, secrets...)
	table, err := modelTable(source, &domain.User{})
	require.NoError(t, err)
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_injection_%t", legacy), func(t *testing.T) {
			confirmationSnapshotRewrite(t, original, path, func(snapshot *ModuleSnapshot) {
				if !legacy {
					return
				}
				var rows []map[string]any
				require.NoError(t, json.Unmarshal(snapshot.Tables[table], &rows))
				for i, row := range rows {
					// Both old wire names and encoding/json's Go-field aliases must
					// remain unable to populate fields whose current tag is "-".
					for _, key := range []string{"confirmation_token", "ConfirmationToken"} {
						row[key] = tokens[i]
					}
					row["confirmation_email"], row["ConfirmationEmail"] = users[i].Email, users[i].Email
					row["confirmation_auth_version"], row["ConfirmationAuthVersion"] = 7, 7
					row["confirmation_sent_at"], row["ConfirmationSentAt"] = now, now
					row["confirmation_issued_at"], row["ConfirmationIssuedAt"] = issued, issued
					row["confirmation_expires_at"], row["ConfirmationExpiresAt"] = expires, expires
				}
				snapshot.Tables[table], err = json.Marshal(rows)
				require.NoError(t, err)
			})
			for attempt := 0; attempt < 2; attempt++ {
				require.NoError(t, RestoreSnapshot(context.Background(), target, "IAM", "confirmation-recovery", path, nil))
				var restored []domain.User
				require.NoError(t, target.Order("id ASC").Find(&restored).Error)
				require.Len(t, restored, len(users))
				for i, user := range restored {
					confirmationSnapshotNoProof(t, user)
					require.Equal(t, users[i].ID, user.ID)
					require.Equal(t, users[i].Email, user.Email)
					require.Equal(t, users[i].Name, user.Name)
					require.True(t, users[i].CreatedAt.Equal(user.CreatedAt))
					require.True(t, users[i].UpdatedAt.Equal(user.UpdatedAt))
					if users[i].ConfirmedAt == nil {
						require.Nil(t, user.ConfirmedAt)
					} else {
						require.NotNil(t, user.ConfirmedAt)
						require.True(t, users[i].ConfirmedAt.Equal(*user.ConfirmedAt))
					}
					confirmationSnapshotRejectLink(t, target, tokens[i])
					require.ErrorIs(t, repository.NewChannelAuthEnterpriseRepository(target).ActivateConfirmationToken(user.ID, tokens[i]), repository.ErrConfirmationInvalid)
				}
				reexport := filepath.Join(t.TempDir(), "iam-reexport.json")
				require.NoError(t, CreateSnapshot(target, "IAM", "confirmation-recovery", reexport))
				confirmationSnapshotNoSecrets(t, confirmationSnapshotRead(t, reexport), secrets...)
			}
		})
	}
	// Recovery invalidates old links, but a new explicitly issued proof still works.
	repo := repository.NewChannelAuthEnterpriseRepository(target)
	fresh, raw, err := repo.GenerateConfirmationToken(users[0].ID)
	require.NoError(t, err)
	require.NotContains(t, tokens, raw)
	require.Nil(t, fresh.ConfirmationIssuedAt)
	require.NoError(t, repo.ActivateConfirmationToken(fresh.ID, raw))
	confirmed, err := repo.ConfirmUserByToken(raw)
	require.NoError(t, err)
	require.NotNil(t, confirmed.ConfirmedAt)
	confirmationSnapshotNoProof(t, *confirmed)
	confirmationSnapshotRejectLink(t, target, raw)
	for _, foreign := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.EmailLog{}, &domain.Message{}} {
		require.False(t, target.Migrator().HasTable(foreign), "IAM restore crossed owner boundary: %T", foreign)
	}
}

func confirmationSnapshotEmailFixtures() ([]domain.EmailLog, []string) {
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	due, attempted := now.Add(time.Minute), now.Add(-time.Minute)
	userID, conversationID := uint(21), uint(71)
	var rows []domain.EmailLog
	var secrets []string
	for i, status := range []string{"failed", "pending", "sent"} {
		raw := strings.Repeat(fmt.Sprintf("%x", i+1), 64)
		link := "https://example.test/auth/confirmation?confirmation_token=" + raw
		rows = append(rows, domain.EmailLog{ID: uint(31 + i), AccountID: uint(91 + i%2), UserID: &userID, EmailType: "confirmation",
			ToEmail: fmt.Sprintf("legacy%d@example.test", i), FromEmail: "confirm@example.test", Subject: "legacy subject " + raw,
			ContentHTML: "<a href=\"" + link + "\">confirm</a>", ContentText: link, Status: status, DeliveryStatus: status,
			Error: "provider echoed " + raw, BounceReason: "bounce echoed " + raw, RetryCount: i, MaxRetries: 9,
			NextRetryAt: &due, LastAttemptAt: &attempted, CreatedAt: now, UpdatedAt: now})
		secrets = append(secrets, raw, link)
	}
	rows = append(rows, domain.EmailLog{ID: 40, AccountID: 91, ConversationID: &conversationID, UserID: &userID, EmailType: "transcript",
		ToEmail: "transcript@example.test", FromEmail: "support@example.test", Subject: "ordinary subject", ContentHTML: "<p>ordinary transcript</p>",
		ContentText: "ordinary transcript", Error: "ordinary provider error", BounceReason: "ordinary bounce detail", Status: "failed", DeliveryStatus: "failed",
		RetryCount: 1, MaxRetries: 5, NextRetryAt: &due, LastAttemptAt: &attempted, CreatedAt: now, UpdatedAt: now})
	return rows, secrets
}

// Normalize location only: never truncate precision or drop any durable field.
func confirmationSnapshotCanonicalEmail(row domain.EmailLog) domain.EmailLog {
	stamp := func(value *time.Time) *time.Time {
		if value == nil {
			return nil
		}
		utc := value.UTC()
		return &utc
	}
	row.CreatedAt, row.UpdatedAt = row.CreatedAt.UTC(), row.UpdatedAt.UTC()
	row.NextRetryAt, row.LastAttemptAt = stamp(row.NextRetryAt), stamp(row.LastAttemptAt)
	return row
}

func confirmationSnapshotRequireEmailRows(t *testing.T, original, actual []domain.EmailLog) {
	t.Helper()
	require.Len(t, actual, len(original))
	for i, row := range actual {
		want := original[i]
		if want.EmailType == "confirmation" {
			want.ContentHTML, want.ContentText, want.Error, want.BounceReason = "", "", "", ""
			want.Subject, want.MaxRetries, want.NextRetryAt = "Email confirmation (legacy content removed)", 0, nil
		}
		require.Equal(t, confirmationSnapshotCanonicalEmail(want), confirmationSnapshotCanonicalEmail(row))
	}
}

func TestConfirmationSnapshotEmailCodecScrubsOnlyConfirmation(t *testing.T) {
	rows, secrets := confirmationSnapshotEmailFixtures()
	before := append([]domain.EmailLog(nil), rows...)
	exported, err := marshalSnapshotRows(rows)
	require.NoError(t, err)
	confirmationSnapshotNoSecrets(t, exported, secrets...)
	require.Equal(t, before, rows, "export scrub mutated live rows")
	var fromExport []domain.EmailLog
	require.NoError(t, json.Unmarshal(exported, &fromExport))
	confirmationSnapshotRequireEmailRows(t, rows, fromExport)
	// Bypass the current writer to simulate a checksum-valid historical file.
	legacy, err := json.Marshal(rows)
	require.NoError(t, err)
	var fromLegacy []domain.EmailLog
	require.NoError(t, unmarshalSnapshotRows(legacy, &fromLegacy))
	confirmationSnapshotRequireEmailRows(t, rows, fromLegacy)
	require.Equal(t, before, rows)
	for _, empty := range [][]domain.EmailLog{nil, {}} {
		raw, err := marshalSnapshotRows(empty)
		require.NoError(t, err)
		var decoded []domain.EmailLog
		require.NoError(t, unmarshalSnapshotRows(raw, &decoded))
		require.Equal(t, empty, decoded)
	}
	// Comparison sensitivity protects PostgreSQL's timezone normalization from
	// becoming an accidental precision or content exclusion.
	control := rows[len(rows)-1]
	differentZone := control
	differentZone.CreatedAt = control.CreatedAt.In(time.FixedZone("snapshot-offset", 5*60*60))
	require.Equal(t, confirmationSnapshotCanonicalEmail(control), confirmationSnapshotCanonicalEmail(differentZone))
	differentZone.CreatedAt = differentZone.CreatedAt.Add(time.Nanosecond)
	require.NotEqual(t, confirmationSnapshotCanonicalEmail(control), confirmationSnapshotCanonicalEmail(differentZone))
}

func TestConfirmationMSGOwnerSnapshotScrubsLegacyMailAndDisablesRetry(t *testing.T) {
	testConfirmationMSGOwnerSnapshotScrubsLegacyMailAndDisablesRetry(t, false)
}

func TestPostgresConfirmationMSGOwnerSnapshotScrubsLegacyMailAndDisablesRetry(t *testing.T) {
	testConfirmationMSGOwnerSnapshotScrubsLegacyMailAndDisablesRetry(t, true)
}

func testConfirmationMSGOwnerSnapshotScrubsLegacyMailAndDisablesRetry(t *testing.T, postgres bool) {
	source, target := confirmationSnapshotDB(t, postgres, "MSG"), confirmationSnapshotDB(t, postgres, "MSG")
	rows, secrets := confirmationSnapshotEmailFixtures()
	require.NoError(t, source.Create(&rows).Error)
	path := filepath.Join(t.TempDir(), "msg-confirmation.json")
	require.NoError(t, CreateSnapshot(source, "MSG", "confirmation-mail-recovery", path))
	original := confirmationSnapshotRead(t, path)
	confirmationSnapshotNoSecrets(t, original, secrets...)
	var unchanged []domain.EmailLog
	require.NoError(t, source.Order("id ASC").Find(&unchanged).Error)
	for i := range rows {
		require.Equal(t, confirmationSnapshotCanonicalEmail(rows[i]), confirmationSnapshotCanonicalEmail(unchanged[i]), "export changed source")
	}
	table, err := modelTable(source, &domain.EmailLog{})
	require.NoError(t, err)
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_file_%t", legacy), func(t *testing.T) {
			confirmationSnapshotRewrite(t, original, path, func(snapshot *ModuleSnapshot) {
				if legacy {
					snapshot.Tables[table], err = json.Marshal(rows)
					require.NoError(t, err)
				}
			})
			for attempt := 0; attempt < 2; attempt++ {
				require.NoError(t, RestoreSnapshot(context.Background(), target, "MSG", "confirmation-mail-recovery", path, nil))
				var restored []domain.EmailLog
				require.NoError(t, target.Order("id ASC").Find(&restored).Error)
				confirmationSnapshotRequireEmailRows(t, rows, restored)
				all, err := json.Marshal(restored)
				require.NoError(t, err)
				confirmationSnapshotNoSecrets(t, all, secrets...)
				// Even the old generic candidate predicate, without an email-type
				// exclusion, must be unable to rediscover a restored proof.
				var candidates []domain.EmailLog
				require.NoError(t, target.Where("status = ? AND retry_count < max_retries AND (next_retry_at IS NULL OR next_retry_at <= ?)", "failed", time.Now().UTC()).Order("id ASC").Find(&candidates).Error)
				require.Len(t, candidates, 1)
				require.Equal(t, "transcript", candidates[0].EmailType)
				candidateJSON, err := json.Marshal(candidates)
				require.NoError(t, err)
				confirmationSnapshotNoSecrets(t, candidateJSON, secrets...)
				logs := repository.NewEmailLogRepository(target)
				for _, row := range restored {
					_, err := logs.Find(row.AccountID+10, row.ID)
					require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				}
				reexport := filepath.Join(t.TempDir(), "msg-reexport.json")
				require.NoError(t, CreateSnapshot(target, "MSG", "confirmation-mail-recovery", reexport))
				confirmationSnapshotNoSecrets(t, confirmationSnapshotRead(t, reexport), secrets...)
			}
			// Only the local capture sender is installed; neither route can send
			// confirmation mail, while the ordinary transcript remains retryable.
			emails, capture := service.NewEmailService(target), service.NewMockEmailSender()
			emails.SetSender(capture)
			for _, row := range rows[:3] {
				updated, sent, err := emails.RetryFailedEmail(context.Background(), row.AccountID, row.ID)
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				require.Nil(t, updated)
				require.False(t, sent)
			}
			require.Empty(t, capture.GetSentEmails())
			count, err := emails.RetryFailedEmails(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			mail := capture.GetSentEmails()
			require.Len(t, mail, 1)
			require.Equal(t, rows[3].ToEmail, mail[0].To)
			require.Equal(t, rows[3].Subject, mail[0].Subject)
			require.Equal(t, rows[3].ContentHTML, mail[0].HTMLBody)
			require.Equal(t, rows[3].ContentText, mail[0].TextBody)
			encoded, err := json.Marshal(mail)
			require.NoError(t, err)
			confirmationSnapshotNoSecrets(t, encoded, secrets...)
		})
	}
	for _, foreign := range []any{&domain.User{}, &domain.UserSession{}, &domain.Account{}, &domain.AccountUser{}} {
		require.False(t, target.Migrator().HasTable(foreign), "MSG restore crossed owner boundary: %T", foreign)
	}
}

func TestConfirmationOwnerSnapshotRollbackAndOwnerBoundaries(t *testing.T) {
	testConfirmationOwnerSnapshotRollbackAndOwnerBoundaries(t, false)
}

func TestPostgresConfirmationOwnerSnapshotRollbackAndOwnerBoundaries(t *testing.T) {
	testConfirmationOwnerSnapshotRollbackAndOwnerBoundaries(t, true)
}

func testConfirmationOwnerSnapshotRollbackAndOwnerBoundaries(t *testing.T, postgres bool) {
	for _, module := range []string{"IAM", "MSG"} {
		t.Run(module, func(t *testing.T) {
			source, target := confirmationSnapshotDB(t, postgres, module), confirmationSnapshotDB(t, postgres, module)
			var verify func()
			if module == "IAM" {
				user := domain.User{ID: 81, Name: "snapshot identity", Email: "snapshot@example.test", PasswordHash: "snapshot password"}
				require.NoError(t, source.Create(&user).Error)
				user.ID, user.Name, user.Email = 0, "live target identity", "live@example.test"
				require.NoError(t, target.Create(&user).Error)
				repo := repository.NewChannelAuthEnterpriseRepository(target)
				_, token, err := repo.GenerateConfirmationToken(user.ID)
				require.NoError(t, err)
				require.NoError(t, repo.ActivateConfirmationToken(user.ID, token))
				var before domain.User
				require.NoError(t, target.First(&before, user.ID).Error)
				verify = func() {
					var after []domain.User
					require.NoError(t, target.Find(&after).Error)
					require.Equal(t, []domain.User{before}, after, "failed restore erased or replaced live confirmation state")
				}
			} else {
				rows, _ := confirmationSnapshotEmailFixtures()
				require.NoError(t, source.Create(&rows).Error)
				sentinel := rows[0]
				sentinel.ID, sentinel.AccountID, sentinel.Subject = 0, 92, "live target mail"
				require.NoError(t, target.Create(&sentinel).Error)
				var before domain.EmailLog
				require.NoError(t, target.First(&before, sentinel.ID).Error)
				verify = func() {
					var after []domain.EmailLog
					require.NoError(t, target.Find(&after).Error)
					require.Equal(t, []domain.EmailLog{before}, after, "failed restore erased or redacted the live target")
				}
			}
			marker := domain.LocalChangeJournal{ChangeID: "confirmation-preexisting", SourceModule: module, Action: "create", ActorType: "System"}
			require.NoError(t, target.Create(&marker).Error)
			verifyAll := func() {
				verify()
				var journals []domain.LocalChangeJournal
				require.NoError(t, target.Find(&journals).Error)
				require.Len(t, journals, 1, "failed restore wrote a completion marker")
				require.Equal(t, marker.ChangeID, journals[0].ChangeID)
			}
			path := filepath.Join(t.TempDir(), strings.ToLower(module)+"-rollback.json")
			require.NoError(t, CreateSnapshot(source, module, "confirmation-rollback", path))
			original := confirmationSnapshotRead(t, path)
			for _, corruption := range []string{"wrong_owner", "foreign_table", "missing_table"} {
				t.Run(corruption, func(t *testing.T) {
					confirmationSnapshotRewrite(t, original, path, func(snapshot *ModuleSnapshot) {
						switch corruption {
						case "wrong_owner":
							snapshot.ModuleID = "TEN"
						case "foreign_table":
							for table := range snapshot.Tables {
								delete(snapshot.Tables, table)
								break
							}
							snapshot.Tables["accounts"] = json.RawMessage(`[]`)
						case "missing_table":
							for table := range snapshot.Tables {
								delete(snapshot.Tables, table)
								break
							}
						}
					})
					require.Error(t, RestoreSnapshot(context.Background(), target, module, "confirmation-rollback", path, nil))
					verifyAll()
				})
			}
			require.NoError(t, os.WriteFile(path, original, 0600))
			rebuildFailure := errors.New("confirmation snapshot rebuild failure")
			require.ErrorIs(t, RestoreSnapshot(context.Background(), target, module, "confirmation-rollback", path, func(context.Context, *gorm.DB) error { return rebuildFailure }), rebuildFailure)
			verifyAll()
			if module == "MSG" {
				injected := errors.New("confirmation retry disable failure")
				called := false
				const callback = "test:confirmation_restore_redaction_failure"
				require.NoError(t, target.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "email_logs" {
						called = true
						_ = tx.AddError(injected)
					}
				}))
				err := RestoreSnapshot(context.Background(), target, module, "confirmation-rollback", path, nil)
				require.NoError(t, target.Callback().Update().Remove(callback))
				require.True(t, called, "restore never explicitly disabled GORM's default retries")
				require.EqualError(t, err, "restore table email_logs: confirmation log cleanup failed")
				require.NotContains(t, err.Error(), injected.Error(), "restore exposed a private storage error")
				verifyAll()
			}
			// The failed rebuild occurs after owner sequence repair; it must not
			// commit the imported high-water mark independently of owner rows.
			if module == "IAM" {
				fresh := domain.User{Name: "fresh target identity", Email: "fresh@example.test", PasswordHash: "fresh password"}
				require.NoError(t, target.Create(&fresh).Error)
				require.Equal(t, uint(2), fresh.ID)
			} else {
				rows, _ := confirmationSnapshotEmailFixtures()
				fresh := rows[3]
				fresh.ID = 0
				require.NoError(t, target.Create(&fresh).Error)
				require.Equal(t, uint(2), fresh.ID)
			}
		})
	}
}
