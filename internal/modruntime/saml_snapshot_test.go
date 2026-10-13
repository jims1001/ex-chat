package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

func samlSnapshotDigest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func samlSnapshotFixtures() (domain.SAMLAuthnRequest, domain.SAMLReplay) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	request := domain.SAMLAuthnRequest{ID: 1, RequestID: "_snapshot_request", AccountID: 91, ConfigVersion: 4,
		ConfigDigest: samlSnapshotDigest("config"), RelayDigest: samlSnapshotDigest("relay"), BrowserSecretDigest: samlSnapshotDigest("browser"),
		CreatedAt: now, ExpiresAt: now.Add(4 * time.Minute)}
	replay := domain.SAMLReplay{ID: 1, AssertionDigest: samlSnapshotDigest("assertion"), ResponseDigest: samlSnapshotDigest("response"),
		CreatedAt: now, RetainUntil: now.Add(5 * time.Minute)}
	return request, replay
}

func TestSAMLSnapshotPrivateCodecsRoundTripAndCoverModelFields(t *testing.T) {
	request, replay := samlSnapshotFixtures()
	for _, pair := range []struct{ model, codec any }{
		{domain.SAMLAuthnRequest{}, samlRequestSnapshot{}},
		{domain.SAMLReplay{}, samlReplaySnapshot{}},
	} {
		model, codec := reflect.TypeOf(pair.model), reflect.TypeOf(pair.codec)
		if model.NumField() != codec.NumField() {
			t.Fatal("private SAML snapshot field inventory drifted")
		}
		for i := 0; i < model.NumField(); i++ {
			field := model.Field(i)
			private, ok := codec.FieldByName(field.Name)
			if !ok || private.Type != field.Type || private.Tag.Get("json") == "" || field.Tag.Get("json") != "-" {
				t.Fatalf("SAML private snapshot field not covered: %s", field.Name)
			}
		}
	}
	for _, consumed := range []bool{false, true} {
		if consumed {
			stamp := request.CreatedAt.Add(time.Minute)
			request.ConsumedAt = &stamp
		}
		raw, err := marshalSnapshotRows([]domain.SAMLAuthnRequest{request})
		if err != nil {
			t.Fatal(err)
		}
		var restored []domain.SAMLAuthnRequest
		if err := unmarshalSnapshotRows(raw, &restored); err != nil || len(restored) != 1 || !reflect.DeepEqual(restored[0], request) {
			t.Fatalf("request private round trip failed: %v", err)
		}
	}
	raw, err := marshalSnapshotRows([]domain.SAMLReplay{replay})
	if err != nil {
		t.Fatal(err)
	}
	var restored []domain.SAMLReplay
	if err := unmarshalSnapshotRows(raw, &restored); err != nil || len(restored) != 1 || !reflect.DeepEqual(restored[0], replay) {
		t.Fatalf("replay private round trip failed: %v", err)
	}
	for _, value := range []any{request, replay} {
		public, err := json.Marshal(value)
		if err != nil || string(public) != "{}" {
			t.Fatalf("public SAML model exposed private state: %s %v", public, err)
		}
	}
}

func TestSAMLSnapshotCodecsRejectAmbiguousAndIncompleteRows(t *testing.T) {
	request, replay := samlSnapshotFixtures()
	for _, fixture := range []struct {
		name, codec string
		rows        any
		target      func() any
	}{
		{"requests", samlRequestSnapshotCodec, []domain.SAMLAuthnRequest{request}, func() any { return &[]domain.SAMLAuthnRequest{request} }},
		{"replays", samlReplaySnapshotCodec, []domain.SAMLReplay{replay}, func() any { return &[]domain.SAMLReplay{replay} }},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			raw, err := marshalSnapshotRows(fixture.rows)
			if err != nil {
				t.Fatal(err)
			}
			var table map[string]json.RawMessage
			if err := json.Unmarshal(raw, &table); err != nil {
				t.Fatal(err)
			}
			var rows []map[string]json.RawMessage
			if err := json.Unmarshal(table["rows"], &rows); err != nil {
				t.Fatal(err)
			}
			bad := map[string][]byte{
				"public_only":         []byte(`[{}]`),
				"unknown_codec":       []byte(`{"codec":"iam.saml.future.v99","rows":[]}`),
				"null_rows":           []byte(`{"codec":"` + fixture.codec + `","rows":null}`),
				"missing_rows":        []byte(`{"codec":"` + fixture.codec + `"}`),
				"case_alias_table":    []byte(strings.Replace(string(raw), `"codec":`, `"Codec":`, 1)),
				"duplicate_table_key": []byte(strings.Replace(string(raw), `"codec":`, `"codec":"`+fixture.codec+`","codec":`, 1)),
				"duplicate_row_key":   []byte(strings.Replace(string(raw), `"id":1`, `"id":1,"id":1`, 1)),
				"case_alias_row":      []byte(strings.Replace(string(raw), `"id":1`, `"ID":1`, 1)),
				"trailing":            append(append([]byte{}, raw...), []byte(` {}`)...),
			}
			for field, old := range rows[0] {
				delete(rows[0], field)
				table["rows"], _ = json.Marshal(rows)
				bad["missing_"+field], _ = json.Marshal(table)
				if field != "consumed_at" {
					rows[0][field] = json.RawMessage(`null`)
					table["rows"], _ = json.Marshal(rows)
					bad["null_"+field], _ = json.Marshal(table)
				}
				rows[0][field] = old
			}
			rows[0]["future_secret"] = json.RawMessage(`"not accepted"`)
			table["rows"], _ = json.Marshal(rows)
			bad["unknown_field"], _ = json.Marshal(table)
			delete(rows[0], "future_secret")
			table["rows"], _ = json.Marshal([]map[string]json.RawMessage{rows[0], rows[0]})
			bad["duplicate_row"], _ = json.Marshal(table)
			for name, input := range bad {
				t.Run(name, func(t *testing.T) {
					target := fixture.target()
					before, _ := marshalSnapshotRows(reflect.ValueOf(target).Elem().Interface())
					if err := unmarshalSnapshotRows(input, target); err == nil {
						t.Fatal("invalid SAML snapshot was accepted")
					}
					after, _ := marshalSnapshotRows(reflect.ValueOf(target).Elem().Interface())
					if string(before) != string(after) {
						t.Fatal("failed decoder changed its target")
					}
				})
			}
		})
	}
	for _, rows := range []any{[]domain.SAMLAuthnRequest{}, []domain.SAMLReplay{}} {
		raw, err := marshalSnapshotRows(rows)
		target := reflect.New(reflect.TypeOf(rows)).Interface()
		if err != nil || unmarshalSnapshotRows(raw, target) != nil || reflect.ValueOf(target).Elem().IsNil() {
			t.Fatal("empty IAM security snapshot did not round trip")
		}
	}
}

func TestSAMLOwnerSnapshotInvalidatesPendingAndRetainsReplayFacts(t *testing.T) {
	testSAMLOwnerSnapshot(t, false)
}

func TestPostgresSAMLOwnerSnapshotInvalidatesPendingAndRetainsReplayFacts(t *testing.T) {
	testSAMLOwnerSnapshot(t, true)
}

func testSAMLOwnerSnapshot(t *testing.T, postgres bool) {
	open := newModuleTestDB
	if postgres {
		open = postgresModuleDB
	}
	source, target := open(t, "IAM"), open(t, "IAM")
	request, replay := samlSnapshotFixtures()
	consumed := request
	consumed.ID, consumed.RequestID, consumed.RelayDigest = 2, "_already_consumed", samlSnapshotDigest("consumed-relay")
	stamp := request.CreatedAt.Add(time.Minute)
	consumed.ConsumedAt = &stamp
	// Allocate fixture IDs normally so PostgreSQL's request sequence is ready
	// for the following repository Prepare, just as in the ordinary write path.
	request.ID, consumed.ID = 0, 0
	if err := source.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&consumed).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&replay).Error; err != nil {
		t.Fatal(err)
	}
	prepared, err := repository.NewSAMLRequestRepository(source).Prepare(context.Background(), "_live_browser_request", repository.SAMLConfigurationBinding{AccountID: 91, ConfigVersion: 4, ConfigDigest: request.ConfigDigest}, 4*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NewSAMLRequestRepository(source).Lookup(context.Background(), prepared.RelayState, prepared.BrowserSecret); err != nil {
		t.Fatal("pending source request should be usable before restore")
	}
	retained := replay
	retained.AssertionDigest, retained.ResponseDigest = samlSnapshotDigest("newer-assertion"), samlSnapshotDigest("newer-response")
	if err := target.Create(&retained).Error; err != nil {
		t.Fatal(err)
	}
	shared := replay
	shared.ID, shared.RetainUntil = 2, replay.RetainUntil.Add(time.Hour)
	if err := target.Create(&shared).Error; err != nil {
		t.Fatal(err)
	}
	// This source pair must also survive despite colliding surrogate IDs on target.
	unique := replay
	unique.ID, unique.AssertionDigest, unique.ResponseDigest = 2, samlSnapshotDigest("backup-assertion"), samlSnapshotDigest("backup-response")
	if err := source.Create(&unique).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "iam.snapshot.json")
	if err := CreateSnapshot(source, "IAM", "saml-owner-restore", path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private SAML snapshot permissions incorrect: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := RestoreSnapshot(context.Background(), target, "IAM", "saml-owner-restore", path, nil); err != nil {
			t.Fatal(err)
		}
		var restored domain.SAMLAuthnRequest
		if err := target.First(&restored, request.ID).Error; err != nil || restored.ConsumedAt == nil || !restored.ConsumedAt.Equal(request.ExpiresAt) || restored.BrowserSecretDigest != request.BrowserSecretDigest || restored.RelayDigest != request.RelayDigest || restored.ConfigDigest != request.ConfigDigest {
			t.Fatalf("pending request not safely invalidated: %v", err)
		}
		restored = domain.SAMLAuthnRequest{}
		if err := target.First(&restored, consumed.ID).Error; err != nil || restored.ConsumedAt == nil || !restored.ConsumedAt.Equal(stamp) {
			t.Fatalf("consumed request revived: %v", err)
		}
		if _, err := repository.NewSAMLRequestRepository(target).Lookup(context.Background(), prepared.RelayState, prepared.BrowserSecret); !errors.Is(err, repository.ErrSAMLRequestInvalid) {
			t.Fatalf("restored browser request was not invalidated: %v", err)
		}
		var facts []domain.SAMLReplay
		if err := target.Find(&facts).Error; err != nil || len(facts) != 3 {
			t.Fatalf("restore lost durable replay facts: %d %v", len(facts), err)
		}
		var merged domain.SAMLReplay
		if err := target.Where("assertion_digest = ?", replay.AssertionDigest).First(&merged).Error; err != nil || !merged.RetainUntil.Equal(shared.RetainUntil) || merged.ID != shared.ID {
			t.Fatalf("restore shortened replay retention or changed retained identity: %v", err)
		}
		for _, original := range []domain.SAMLReplay{retained, replay, unique} {
			for _, assertion := range []bool{true, false} {
				duplicate := original
				duplicate.ID = 0
				if assertion {
					duplicate.ResponseDigest = samlSnapshotDigest("fresh-response")
				} else {
					duplicate.AssertionDigest = samlSnapshotDigest("fresh-assertion")
				}
				if err := target.Create(&duplicate).Error; err == nil {
					t.Fatal("restored assertion or response became replayable")
				}
			}
		}
	}
	for _, foreign := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.Inbox{}, &domain.Message{}} {
		if target.Migrator().HasTable(foreign) {
			t.Fatalf("IAM restore created foreign owner table: %T", foreign)
		}
	}
	// Both owner sequences must remain writable after explicit-ID restoration.
	// Merely fixing fixture insertion would miss a PostgreSQL restore regression.
	var requestMax, replayMax uint
	if err := target.Model(&domain.SAMLAuthnRequest{}).Select("MAX(id)").Scan(&requestMax).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Model(&domain.SAMLReplay{}).Select("MAX(id)").Scan(&replayMax).Error; err != nil {
		t.Fatal(err)
	}
	fresh, err := repository.NewSAMLRequestRepository(target).Prepare(context.Background(), "_fresh_after_restore", repository.SAMLConfigurationBinding{AccountID: 91, ConfigVersion: 4, ConfigDigest: request.ConfigDigest}, 4*time.Minute)
	if err != nil {
		t.Fatalf("cannot create fresh request after restore: %v", err)
	}
	var freshRequest domain.SAMLAuthnRequest
	if err := target.Where("request_id = ?", fresh.RequestID).First(&freshRequest).Error; err != nil || freshRequest.ID <= requestMax || freshRequest.ConsumedAt != nil {
		t.Fatalf("request sequence did not advance beyond restored maximum: id=%d max=%d err=%v", freshRequest.ID, requestMax, err)
	}
	if _, err := repository.NewSAMLRequestRepository(target).Lookup(context.Background(), fresh.RelayState, fresh.BrowserSecret); err != nil {
		t.Fatalf("fresh post-restore browser request is not usable: %v", err)
	}
	freshReplay := replay
	freshReplay.ID, freshReplay.AssertionDigest, freshReplay.ResponseDigest = 0, samlSnapshotDigest("fresh-post-restore-assertion"), samlSnapshotDigest("fresh-post-restore-response")
	if err := target.Create(&freshReplay).Error; err != nil || freshReplay.ID <= replayMax {
		t.Fatalf("replay sequence did not advance beyond restored maximum: id=%d max=%d err=%v", freshReplay.ID, replayMax, err)
	}
}

func rewriteSAMLSnapshot(t *testing.T, original []byte, path string, mutate func(*ModuleSnapshot)) {
	t.Helper()
	var file snapshotFile
	if err := json.Unmarshal(original, &file); err != nil {
		t.Fatal(err)
	}
	mutate(&file.Snapshot)
	canonical, err := json.Marshal(file.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	file.Checksum = hex.EncodeToString(sum[:])
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSAMLSnapshotCorruptionAndReplayConflictsRollBackAtomically(t *testing.T) {
	testSAMLSnapshotRollback(t, false)
}

func TestPostgresSAMLSnapshotCorruptionAndReplayConflictsRollBackAtomically(t *testing.T) {
	testSAMLSnapshotRollback(t, true)
}

func testSAMLSnapshotRollback(t *testing.T, postgres bool) {
	open := newModuleTestDB
	if postgres {
		open = postgresModuleDB
	}
	source, target := open(t, "IAM"), open(t, "IAM")
	request, replay := samlSnapshotFixtures()
	if err := source.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&replay).Error; err != nil {
		t.Fatal(err)
	}
	sentinel := request
	sentinel.ID, sentinel.RequestID, sentinel.RelayDigest = 33, "_target_pending", samlSnapshotDigest("target-relay")
	if err := target.Create(&sentinel).Error; err != nil {
		t.Fatal(err)
	}
	guard := replay
	guard.ID, guard.AssertionDigest, guard.ResponseDigest = 33, samlSnapshotDigest("guard-assertion"), samlSnapshotDigest("guard-response")
	if err := target.Create(&guard).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "iam-corrupt.snapshot.json")
	if err := CreateSnapshot(source, "IAM", "saml-corrupt", path); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	requestTable, _ := modelTable(source, &domain.SAMLAuthnRequest{})
	replayTable, _ := modelTable(source, &domain.SAMLReplay{})
	cases := map[string]func(*ModuleSnapshot){
		"missing_table":  func(s *ModuleSnapshot) { delete(s.Tables, replayTable) },
		"public_request": func(s *ModuleSnapshot) { s.Tables[requestTable] = json.RawMessage(`[{}]`) },
		"public_replay":  func(s *ModuleSnapshot) { s.Tables[replayTable] = json.RawMessage(`[{}]`) },
		"zero_request_id": func(s *ModuleSnapshot) {
			bad := request
			bad.ID = 0
			s.Tables[requestTable], _ = marshalSnapshotRows([]domain.SAMLAuthnRequest{bad})
		},
		"invalid_request_id": func(s *ModuleSnapshot) {
			bad := request
			bad.RequestID = "invalid id"
			s.Tables[requestTable], _ = marshalSnapshotRows([]domain.SAMLAuthnRequest{bad})
		},
		"missing_browser_digest": func(s *ModuleSnapshot) {
			bad := request
			bad.BrowserSecretDigest = ""
			s.Tables[requestTable], _ = marshalSnapshotRows([]domain.SAMLAuthnRequest{bad})
		},
		"overlong_request": func(s *ModuleSnapshot) {
			bad := request
			bad.ExpiresAt = bad.CreatedAt.Add(6 * time.Minute)
			s.Tables[requestTable], _ = marshalSnapshotRows([]domain.SAMLAuthnRequest{bad})
		},
		"bad_replay_retention": func(s *ModuleSnapshot) {
			bad := replay
			bad.RetainUntil = bad.CreatedAt.Add(-time.Second)
			s.Tables[replayTable], _ = marshalSnapshotRows([]domain.SAMLReplay{bad})
		},
		"assertion_pair_conflict": func(s *ModuleSnapshot) {
			bad := replay
			bad.AssertionDigest = guard.AssertionDigest
			s.Tables[replayTable], _ = marshalSnapshotRows([]domain.SAMLReplay{bad})
		},
		"response_pair_conflict": func(s *ModuleSnapshot) {
			bad := replay
			bad.ResponseDigest = guard.ResponseDigest
			s.Tables[replayTable], _ = marshalSnapshotRows([]domain.SAMLReplay{bad})
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rewriteSAMLSnapshot(t, original, path, mutate)
			if err := RestoreSnapshot(context.Background(), target, "IAM", "saml-corrupt", path, nil); err == nil {
				t.Fatal("corrupt IAM security snapshot was accepted")
			}
			var after domain.SAMLAuthnRequest
			if err := target.First(&after, sentinel.ID).Error; err != nil || after.RequestID != sentinel.RequestID || after.ConsumedAt != nil {
				t.Fatalf("failed restore mutated target request: %v", err)
			}
			var requests, replays, markers int64
			for model, count := range map[any]*int64{&domain.SAMLAuthnRequest{}: &requests, &domain.SAMLReplay{}: &replays, &domain.LocalChangeJournal{}: &markers} {
				if err := target.Model(model).Count(count).Error; err != nil {
					t.Fatal(err)
				}
			}
			if requests != 1 || replays != 1 || markers != 0 {
				t.Fatalf("invalid restore partially committed: requests=%d replays=%d markers=%d", requests, replays, markers)
			}
		})
	}
	// Rebuild failure occurs after the replay merge, and must undo it as well.
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSnapshot(context.Background(), target, "IAM", "saml-corrupt", path, func(context.Context, *gorm.DB) error { return errors.New("rebuild failed") }); err == nil {
		t.Fatal("failed rebuild unexpectedly succeeded")
	}
	var replayCount int64
	if err := target.Model(&domain.SAMLReplay{}).Count(&replayCount).Error; err != nil || replayCount != 1 {
		t.Fatalf("failed rebuild committed replay merge: %d %v", replayCount, err)
	}
}
