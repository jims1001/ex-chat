package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var dataImportStorageFailure = errors.New("data-import-private-storage-diagnostic")

const dataImportValidCSV = "name,email\nImport One,one@import.example.test\n"

type importDurabilityFixture struct {
	mig, cus      *gorm.DB
	h             *AuthEnterpriseHandler
	r             *gin.Engine
	contactWrites int
}

func newImportDurabilityFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *importDurabilityFixture {
	t.Helper()
	f := &importDurabilityFixture{mig: open(t, "MIG"), cus: open(t, "CUS")}
	require.NoError(t, f.mig.AutoMigrate(&domain.DataImport{}))
	require.NoError(t, f.cus.AutoMigrate(&domain.Contact{}))
	require.False(t, f.mig.Migrator().HasTable("contacts"))
	require.False(t, f.cus.Migrator().HasTable("data_imports"))
	repo := repository.NewChannelAuthEnterpriseRepositoryWithOwnerStores(nil, nil, f.mig, nil, nil, nil, nil)
	f.h = &AuthEnterpriseHandler{migRepo: repo, dataImportService: service.NewDataImportServiceWithContactOwner(f.mig, repository.NewContactRepositoryWithOwnerPorts(f.cus, nil, nil, nil, nil, nil))}
	gin.SetMode(gin.TestMode)
	f.r = gin.New()
	f.r.POST("/accounts/:account_id/data_imports", f.h.CreateDataImport)
	f.r.GET("/accounts/:account_id/data_imports/:id", f.h.GetDataImport)
	f.r.POST("/accounts/:account_id/data_imports/:id/start", f.h.StartDataImport)
	f.r.POST("/accounts/:account_id/data_imports/:id/execute", f.h.StartDataImport)
	f.r.POST("/accounts/:account_id/data_imports/:id/prevalidate", f.h.PrevalidateDataImport)
	f.r.POST("/accounts/:account_id/data_imports/:id/cancel", f.h.CancelDataImport)
	f.r.POST("/accounts/:account_id/data_imports/:id/discard", f.h.DiscardDataImport)
	require.NoError(t, f.cus.Callback().Create().Before("gorm:create").Register("import_contact_write_observer", func(tx *gorm.DB) {
		if tx.Statement.Table == "contacts" {
			f.contactWrites++
		}
	}))
	t.Cleanup(func() { require.NoError(t, f.cus.Callback().Create().Remove("import_contact_write_observer")) })
	return f
}

func (f *importDurabilityFixture) request(method, path string, payload any) *httptest.ResponseRecorder {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			panic(err)
		}
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	f.r.ServeHTTP(out, req)
	return out
}
func importPayload(raw string) map[string]any {
	return map[string]any{"source_provider": "csv", "import_type": "contacts", "raw_data": raw}
}
func importResponse(t *testing.T, out *httptest.ResponseRecorder, status int) domain.DataImport {
	t.Helper()
	require.Equal(t, status, out.Code, out.Body.String())
	var body struct {
		Success bool              `json:"success"`
		Data    domain.DataImport `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.NotZero(t, body.Data.ID)
	return body.Data
}
func assertImportStored(t *testing.T, response, stored domain.DataImport) {
	t.Helper()
	require.WithinDuration(t, response.CreatedAt, stored.CreatedAt, time.Microsecond)
	require.WithinDuration(t, response.UpdatedAt, stored.UpdatedAt, time.Microsecond)
	response.CreatedAt, response.UpdatedAt = time.Time{}, time.Time{}
	stored.CreatedAt, stored.UpdatedAt = time.Time{}, time.Time{}
	require.Equal(t, response, stored)
}

func assertImportFailure(t *testing.T, out *httptest.ResponseRecorder, message string) {
	t.Helper()
	require.Equal(t, http.StatusInternalServerError, out.Code, out.Body.String())
	require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, message), out.Body.String())
	require.NotContains(t, out.Body.String(), dataImportStorageFailure.Error())
}
func (f *importDurabilityFixture) rows(t *testing.T) []domain.DataImport {
	t.Helper()
	var rows []domain.DataImport
	require.NoError(t, f.mig.Order("id").Find(&rows).Error)
	return rows
}
func (f *importDurabilityFixture) contacts(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, f.cus.Model(&domain.Contact{}).Count(&count).Error)
	return count
}
func (f *importDurabilityFixture) seed(t *testing.T, raw string) domain.DataImport {
	t.Helper()
	imp := domain.DataImport{AccountID: 91, ImportType: "contacts", SourceProvider: "csv", RawData: raw, Status: "staged"}
	require.NoError(t, f.mig.Create(&imp).Error)
	return imp
}

// Fail one exact save, so later successes cannot disguise an ignored error.
func failImportWrite(t *testing.T, db *gorm.DB, create bool, at int) (*int, func()) {
	t.Helper()
	attempts := 0
	active := true
	callback := func(tx *gorm.DB) {
		if active && tx.Statement.Table == "data_imports" {
			attempts++
			if attempts == at {
				_ = tx.AddError(dataImportStorageFailure)
			}
		}
	}
	const name = "import_durability_failure"
	if create {
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, callback))
	} else {
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, callback))
	}
	t.Cleanup(func() {
		if create {
			require.NoError(t, db.Callback().Create().Remove(name))
		} else {
			require.NoError(t, db.Callback().Update().Remove(name))
		}
	})
	return &attempts, func() { active = false }
}

func TestDataImportDurabilityOwners(t *testing.T) {
	runDataImportDurabilityOwners(t, func(t *testing.T, owner string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, IgnoreRelationshipsWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		conn, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		return db
	})
}

func runDataImportDurabilityOwners(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("admission_failure", func(t *testing.T) {
		for _, kind := range []string{"auto", "staged", "count_only", "invalid_json"} {
			t.Run(kind, func(t *testing.T) {
				f := newImportDurabilityFixture(t, open)
				payload := importPayload(dataImportValidCSV)
				switch kind {
				case "staged":
					payload["auto_start"] = false
				case "count_only":
					payload["raw_data"] = ""
					payload["total_records"] = 250
				case "invalid_json":
					payload["source_provider"] = "json"
					payload["raw_data"] = "[{"
				}
				attempts, recover := failImportWrite(t, f.mig, true, 1)
				assertImportFailure(t, f.request(http.MethodPost, "/accounts/91/data_imports", payload), "Failed to create import task")
				require.Equal(t, 1, *attempts)
				require.Empty(t, f.rows(t))
				require.Zero(t, f.contactWrites)
				require.Zero(t, f.contacts(t))
				// No side effects occurred, so a new explicit request after recovery is safe.
				recover()
				out := f.request(http.MethodPost, "/accounts/91/data_imports", payload)
				if kind == "invalid_json" {
					require.Equal(t, http.StatusBadRequest, out.Code)
					require.Equal(t, "failed", f.rows(t)[0].Status)
				} else {
					importResponse(t, out, http.StatusCreated)
				}
			})
		}
	})
	t.Run("execution_save_failure", func(t *testing.T) {
		for _, entry := range []string{"auto", "start"} {
			t.Run(entry, func(t *testing.T) {
				for _, stage := range []string{"processing", "final", "handler_final", "empty_failure"} {
					t.Run(stage, func(t *testing.T) {
						f := newImportDurabilityFixture(t, open)
						raw := dataImportValidCSV
						at := 1
						message := "Failed to execute import"
						switch stage {
						case "final":
							at = 2
						case "handler_final":
							at = 3
							message = "Failed to update import task"
						case "empty_failure":
							at = 2
							raw = ""
						}
						path := "/accounts/91/data_imports"
						var payload any = importPayload(raw)
						if entry == "start" {
							imp := f.seed(t, raw)
							path = fmt.Sprintf("/accounts/91/data_imports/%d/start", imp.ID)
							payload = nil
						}
						attempts, recover := failImportWrite(t, f.mig, false, at)
						assertImportFailure(t, f.request(http.MethodPost, path, payload), message)
						require.Equal(t, at, *attempts, "no later save may hide the failed write")
						rows := f.rows(t)
						require.Len(t, rows, 1)
						wantStatus := "processing"
						wantContacts := int64(1)
						if stage == "processing" {
							wantStatus = "staged"
							if entry == "auto" {
								wantStatus = "validated"
							}
							wantContacts = 0
						}
						if stage == "empty_failure" {
							wantContacts = 0
						}
						if stage == "handler_final" {
							wantStatus = "completed"
						}
						require.Equal(t, wantStatus, rows[0].Status)
						require.Equal(t, wantContacts, f.contacts(t))
						require.Equal(t, int(wantContacts), f.contactWrites)
						recover()
						if stage == "processing" {
							// The failed processing save admitted no work. An explicit start can recover.
							out := f.request(http.MethodPost, fmt.Sprintf("/accounts/91/data_imports/%d/start", rows[0].ID), nil)
							done := importResponse(t, out, http.StatusOK)
							require.Equal(t, "completed", done.Status)
							require.Equal(t, 1, done.ProcessedRecords)
							require.Equal(t, int64(1), f.contacts(t))
						} else {
							// Read the durable state only. Do not re-execute an uncertain post-write task.
							got := importResponse(t, f.request(http.MethodGet, fmt.Sprintf("/accounts/91/data_imports/%d", rows[0].ID), nil), http.StatusOK)
							require.Equal(t, wantStatus, got.Status)
							require.Equal(t, wantContacts, f.contacts(t))
							require.Equal(t, int(wantContacts), f.contactWrites)
						}
					})
				}
			})
		}
	})
	t.Run("service_returns_original_save_error", func(t *testing.T) {
		for _, stage := range []string{"processing", "empty_failure", "final"} {
			t.Run(stage, func(t *testing.T) {
				f := newImportDurabilityFixture(t, open)
				raw := dataImportValidCSV
				at := 1
				if stage != "processing" {
					at = 2
				}
				if stage == "empty_failure" {
					raw = ""
				}
				imp := f.seed(t, raw)
				attempts, _ := failImportWrite(t, f.mig, false, at)
				err := f.h.dataImportService.ExecuteImport(context.Background(), 91, &imp)
				require.ErrorIs(t, err, dataImportStorageFailure)
				require.Equal(t, at, *attempts)
				if stage != "final" {
					require.Zero(t, f.contactWrites)
					require.Zero(t, f.contacts(t))
				}
			})
		}
	})
	t.Run("mutation_save_failure", func(t *testing.T) {
		for _, action := range []string{"prevalidate", "cancel", "discard"} {
			t.Run(action, func(t *testing.T) {
				f := newImportDurabilityFixture(t, open)
				imp := f.seed(t, dataImportValidCSV)
				before := f.rows(t)
				attempts, recover := failImportWrite(t, f.mig, false, 1)
				path := fmt.Sprintf("/accounts/91/data_imports/%d/%s", imp.ID, action)
				assertImportFailure(t, f.request(http.MethodPost, path, nil), "Failed to update import task")
				require.Equal(t, 1, *attempts)
				require.Equal(t, before, f.rows(t))
				require.Zero(t, f.contactWrites)
				recover()
				out := f.request(http.MethodPost, path, nil)
				require.Equal(t, http.StatusOK, out.Code, out.Body.String())
				want := "discarded"
				if action == "prevalidate" {
					want = "validated"
				}
				require.Equal(t, want, f.rows(t)[0].Status)
				require.Zero(t, f.contacts(t))
			})
		}
	})
	t.Run("count_only_is_pending", func(t *testing.T) {
		f := newImportDurabilityFixture(t, open)
		f.h.dataImportService = nil
		got := importResponse(t, f.request(http.MethodPost, "/accounts/91/data_imports", map[string]any{"total_records": 250}), http.StatusCreated)
		require.Equal(t, "pending", got.Status)
		require.Equal(t, 250, got.TotalRecords)
		require.Zero(t, got.ProcessedRecords)
		require.Zero(t, got.FailedRecords)
		require.Zero(t, got.SkippedRecords)
		assertImportStored(t, got, f.rows(t)[0])
		require.Zero(t, f.contactWrites)
	})
	t.Run("healthy_partial_results", func(t *testing.T) {
		for _, provider := range []string{"csv", "json"} {
			t.Run(provider, func(t *testing.T) {
				f := newImportDurabilityFixture(t, open)
				require.NoError(t, f.cus.Create(&domain.Contact{AccountID: 92, Name: "Foreign", Email: "one@import.example.test"}).Error)
				f.contactWrites = 0
				raw := dataImportValidCSV + "Bad,invalid-email\nDuplicate,one@import.example.test\n"
				if provider == "json" {
					raw = `[{"name":"Import One","email":"one@import.example.test"},{"email":"invalid-email"},{"email":"one@import.example.test"}]`
				}
				payload := importPayload(raw)
				payload["source_provider"] = provider
				got := importResponse(t, f.request(http.MethodPost, "/accounts/91/data_imports", payload), http.StatusCreated)
				require.Equal(t, "completed", got.Status, "preserve existing partial-result status semantics")
				require.Equal(t, 3, got.TotalRecords)
				require.Equal(t, 1, got.ProcessedRecords)
				require.Equal(t, 1, got.FailedRecords)
				require.Equal(t, 1, got.SkippedRecords)
				require.Contains(t, got.ErrorsJSON, "Invalid email")
				require.Contains(t, got.SkippedJSON, "Duplicate")
				require.Equal(t, int64(2), f.contacts(t))
				require.Equal(t, 1, f.contactWrites)
				assertImportStored(t, got, f.rows(t)[0])
			})
		}
	})
	t.Run("partial_result_final_save_failure", func(t *testing.T) {
		f := newImportDurabilityFixture(t, open)
		attempts, _ := failImportWrite(t, f.mig, false, 2)
		raw := dataImportValidCSV + "Bad,invalid-email\n"
		assertImportFailure(t, f.request(http.MethodPost, "/accounts/91/data_imports", importPayload(raw)), "Failed to execute import")
		require.Equal(t, 2, *attempts)
		require.Equal(t, 1, f.contactWrites)
		require.Equal(t, int64(1), f.contacts(t))
		require.Equal(t, "processing", f.rows(t)[0].Status)
		require.Zero(t, f.rows(t)[0].ProcessedRecords, "unpersisted final counts must not be claimed durable")
	})
	t.Run("empty_execution_is_failed", func(t *testing.T) {
		for _, entry := range []string{"auto", "start"} {
			t.Run(entry, func(t *testing.T) {
				f := newImportDurabilityFixture(t, open)
				path := "/accounts/91/data_imports"
				var payload any = importPayload("")
				if entry == "start" {
					imp := f.seed(t, "")
					path = fmt.Sprintf("/accounts/91/data_imports/%d/start", imp.ID)
					payload = nil
				}
				assertImportFailure(t, f.request(http.MethodPost, path, payload), "Failed to execute import")
				rows := f.rows(t)
				require.Len(t, rows, 1)
				require.Equal(t, "failed", rows[0].Status)
				require.Contains(t, rows[0].ErrorsJSON, "No data to import")
				require.Zero(t, rows[0].ProcessedRecords)
				require.Zero(t, f.contactWrites)
			})
		}
	})
	t.Run("zero_results_not_completed", func(t *testing.T) {
		for _, kind := range []string{"csv_headers", "json_empty", "invalid_rows"} {
			t.Run(kind, func(t *testing.T) {
				f := newImportDurabilityFixture(t, open)
				raw := "name,email\n"
				if kind == "json_empty" {
					raw = "[]"
				}
				if kind == "invalid_rows" {
					raw = `[{"invalid_field_only":true}]`
				}
				got := importResponse(t, f.request(http.MethodPost, "/accounts/91/data_imports", importPayload(raw)), http.StatusCreated)
				require.Equal(t, "failed", got.Status)
				require.Zero(t, got.ProcessedRecords)
				require.Zero(t, f.contactWrites)
			})
		}
	})
	t.Run("stage_start_and_terminal_guards", func(t *testing.T) {
		f := newImportDurabilityFixture(t, open)
		payload := importPayload(dataImportValidCSV)
		payload["action"] = "stage"
		staged := importResponse(t, f.request(http.MethodPost, "/accounts/91/data_imports", payload), http.StatusCreated)
		require.Equal(t, "validated", staged.Status)
		require.Zero(t, staged.ProcessedRecords)
		require.Zero(t, f.contactWrites)
		path := fmt.Sprintf("/accounts/91/data_imports/%d/execute", staged.ID)
		done := importResponse(t, f.request(http.MethodPost, path, nil), http.StatusOK)
		require.Equal(t, "completed", done.Status)
		require.Equal(t, 1, done.ProcessedRecords)
		require.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, path, nil).Code)
		require.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, fmt.Sprintf("/accounts/91/data_imports/%d/discard", staged.ID), nil).Code)
		empty := importPayload("")
		empty["auto_start"] = false
		empty["total_records"] = 250
		staged = importResponse(t, f.request(http.MethodPost, "/accounts/91/data_imports", empty), http.StatusCreated)
		require.Equal(t, "staged", staged.Status)
		require.Zero(t, staged.ProcessedRecords)
	})
	t.Run("database_write_errors", func(t *testing.T) {
		for _, stage := range []string{"create", "processing", "final"} {
			t.Run(stage, func(t *testing.T) {
				f := newImportDurabilityFixture(t, open)
				var imp domain.DataImport
				if stage == "processing" {
					imp = f.seed(t, dataImportValidCSV)
				}
				if stage == "final" {
					require.NoError(t, f.cus.Callback().Create().After("gorm:create").Register("make_import_table_unavailable", func(tx *gorm.DB) {
						if tx.Statement.Table == "contacts" {
							require.NoError(t, f.mig.Migrator().RenameTable("data_imports", "unavailable_data_imports"))
						}
					}))
					t.Cleanup(func() { require.NoError(t, f.cus.Callback().Create().Remove("make_import_table_unavailable")) })
				} else {
					require.NoError(t, f.mig.Migrator().RenameTable("data_imports", "unavailable_data_imports"))
				}
				if stage == "processing" {
					require.Error(t, f.h.dataImportService.ExecuteImport(context.Background(), 91, &imp))
				} else {
					message := "Failed to create import task"
					if stage == "final" {
						message = "Failed to execute import"
					}
					assertImportFailure(t, f.request(http.MethodPost, "/accounts/91/data_imports", importPayload(dataImportValidCSV)), message)
				}
				require.NoError(t, f.mig.Migrator().RenameTable("unavailable_data_imports", "data_imports"))
				wantContacts := int64(0)
				if stage == "final" {
					wantContacts = 1
					require.Equal(t, "processing", f.rows(t)[0].Status)
				}
				require.Equal(t, wantContacts, f.contacts(t))
				require.Equal(t, int(wantContacts), f.contactWrites)
				if stage == "create" {
					require.Empty(t, f.rows(t))
				}
				if stage == "processing" {
					require.Equal(t, "staged", f.rows(t)[0].Status)
				}
			})
		}
	})
	t.Run("contact_write_failure_result_contract", func(t *testing.T) {
		for _, kind := range []string{"partial", "all"} {
			t.Run(kind, func(t *testing.T) {
				f := newImportDurabilityFixture(t, open)
				calls := 0
				require.NoError(t, f.cus.Callback().Create().Before("gorm:create").Register("fail_import_contact", func(tx *gorm.DB) {
					if tx.Statement.Table == "contacts" {
						calls++
						if kind == "all" || calls == 2 {
							_ = tx.AddError(errors.New("synthetic contact write failure"))
						}
					}
				}))
				t.Cleanup(func() { require.NoError(t, f.cus.Callback().Create().Remove("fail_import_contact")) })
				got := importResponse(t, f.request(http.MethodPost, "/accounts/91/data_imports", importPayload(dataImportValidCSV+"Second,two@import.example.test\n")), http.StatusCreated)
				wantProcessed, wantFailed, wantStatus := 1, 1, "completed"
				if kind == "all" {
					wantProcessed, wantFailed, wantStatus = 0, 2, "failed"
				}
				require.Equal(t, 2, calls)
				require.Equal(t, wantStatus, got.Status)
				require.Equal(t, wantProcessed, got.ProcessedRecords)
				require.Equal(t, wantFailed, got.FailedRecords)
				require.Equal(t, 2, got.TotalRecords)
				require.Contains(t, got.ErrorsJSON, "Database insert failed")
				require.Equal(t, int64(wantProcessed), f.contacts(t))
			})
		}
	})
	t.Run("foreign_task_mutations_stay_not_found", func(t *testing.T) {
		f := newImportDurabilityFixture(t, open)
		imp := f.seed(t, dataImportValidCSV)
		before := f.rows(t)
		for _, action := range []string{"start", "execute", "prevalidate", "cancel", "discard"} {
			out := f.request(http.MethodPost, fmt.Sprintf("/accounts/92/data_imports/%d/%s", imp.ID, action), nil)
			require.Equal(t, http.StatusNotFound, out.Code, out.Body.String())
		}
		require.Equal(t, before, f.rows(t))
		require.Zero(t, f.contactWrites)
	})
}
