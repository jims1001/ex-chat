package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type srhConcurrentResult struct {
	index  int
	status int
	body   string
}

// Each fixture owns a separate registry and receipt store. Sharing only the
// native database proves that a process-local mutex cannot provide the CAS.
func srhConcurrentCommands(t *testing.T, fixtures []*srhFixture, commands []foundation.CommandEnvelope) []srhConcurrentResult {
	t.Helper()
	handlers := make([]http.Handler, len(fixtures))
	for i, fixture := range fixtures {
		fixture.runtime.Config.MaxTimeout = 10 * time.Second
		handlers[i] = fixture.runtime.Handler()
	}
	start := make(chan struct{})
	done := make(chan srhConcurrentResult, len(commands))
	for i, command := range commands {
		command.TimeoutPolicy.TimeoutMS = 10000
		fixtureIndex := i % len(fixtures)
		go func(index int, command foundation.CommandEnvelope, fixtureIndex int) {
			<-start
			response := performContractRequest(handlers[fixtureIndex], "/contracts/commands/"+command.CommandType, command, func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+fixtures[fixtureIndex].runtime.Config.AuthToken)
				request.Header.Set(headerCaller, "API")
				request.Header.Set(headerCorrelationID, command.CorrelationID)
			})
			done <- srhConcurrentResult{index: index, status: response.Code, body: response.Body.String()}
		}(i, command, fixtureIndex)
	}
	close(start)
	results := make([]srhConcurrentResult, 0, len(commands))
	for range commands {
		select {
		case result := <-done:
			results = append(results, result)
		case <-time.After(20 * time.Second):
			t.Fatal("concurrent saved-filter command did not terminate")
		}
	}
	return results
}

func srhRecoveryMutation(key, operation string, id uint, version uint64) foundation.CommandEnvelope {
	command := srhCommand(key, operation)
	command.Target = foundation.ContractTarget{Type: "custom_filter", ID: strconv.FormatUint(uint64(id), 10), ExpectedVersion: version}
	command.Input = map[string]any{"operation": operation}
	if operation == "update" {
		command.Input["name"] = "private-" + key
		command.Input["query"] = json.RawMessage(`{"private_marker":"private-query-value","integer":9007199254740993}`)
	}
	return command
}

func srhRecoveryCounts(t *testing.T, db *gorm.DB, filters, receipts, events int64) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&domain.CustomFilter{}).Count(&count).Error)
	require.Equal(t, filters, count, "saved-filter facts")
	for action, want := range map[string]int64{"command_receipt": receipts, "event_outbox": events} {
		require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("action = ?", action).Count(&count).Error)
		require.Equal(t, want, count, action)
	}
	require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("action IN ?", []string{"create", "update", "delete"}).Count(&count).Error)
	require.Equal(t, events, count, "business change journal")
}

func srhRecoveryPrivateEvents(t *testing.T, db *gorm.DB) []domain.LocalChangeJournal {
	t.Helper()
	var rows []domain.LocalChangeJournal
	require.NoError(t, db.Where("action = ?", "event_outbox").Order("id ASC").Find(&rows).Error)
	for _, row := range rows {
		var event foundation.EventEnvelope
		require.NoError(t, json.Unmarshal([]byte(row.AfterState), &event))
		require.NoError(t, event.Validate())
		require.Equal(t, "SRH", event.SourceModule)
		require.Equal(t, "srh.custom_filter.changed.v1", event.EventType)
		require.Equal(t, "User", event.Actor.Type)
		require.NotEmpty(t, event.Payload["user_id"])
		require.NotContains(t, event.Payload, "query")
		require.NotContains(t, event.Payload, "name")
		require.NotContains(t, row.AfterState, "private-query-value")
		require.NotContains(t, row.AfterState, "private-snapshot")
	}
	return rows
}

func TestPostgresSRHConcurrentCAS(t *testing.T) {
	for _, operations := range [][2]string{{"update", "update"}, {"update", "delete"}, {"delete", "delete"}} {
		t.Run(strings.Join(operations[:], "_"), func(t *testing.T) {
			db := postgresModuleDB(t, "SRH")
			fixtures := []*srhFixture{newSRHFixture(t, db), newSRHFixture(t, db)}
			created := srhDecodeView(t, fixtures[0].command(t, srhCommand("race-create", "create"), http.StatusOK))
			commands := []foundation.CommandEnvelope{
				srhRecoveryMutation("race-left", operations[0], created.ID, 1),
				srhRecoveryMutation("race-right", operations[1], created.ID, 1),
			}
			results := srhConcurrentCommands(t, fixtures, commands)
			winner := -1
			for _, result := range results {
				if result.status == http.StatusOK {
					require.Equal(t, -1, winner, "two commands with the same expected version committed: %+v", results)
					winner = result.index
					continue
				}
				if operations == [2]string{"update", "update"} {
					require.Equal(t, http.StatusConflict, result.status, result.body)
				} else {
					require.Contains(t, []int{http.StatusConflict, http.StatusNotFound}, result.status, result.body)
				}
			}
			require.NotEqual(t, -1, winner, "neither concurrent command committed: %+v", results)
			var filters int64
			if operations[winner] == "update" {
				filters = 1
				var row domain.CustomFilter
				require.NoError(t, db.First(&row, created.ID).Error)
				require.EqualValues(t, 2, row.ObjectVersion)
				require.Equal(t, commands[winner].Input["name"], row.Name)
			}
			srhRecoveryCounts(t, db, filters, 2, 2)
			events := srhRecoveryPrivateEvents(t, db)
			require.Equal(t, 2, events[1].ObjectVersionAfter)
			for _, foreign := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.User{}, &domain.Contact{}, &domain.Conversation{}} {
				require.False(t, db.Migrator().HasTable(foreign), "SRH created foreign table %T", foreign)
			}
		})
	}

	t.Run("same_idempotency_key", func(t *testing.T) {
		db := postgresModuleDB(t, "SRH")
		fixtures := []*srhFixture{newSRHFixture(t, db), newSRHFixture(t, db)}
		var id uint
		for i, operation := range []string{"create", "update", "delete"} {
			command := srhCommand("duplicate-"+operation, operation)
			if operation != "create" {
				command = srhRecoveryMutation("duplicate-"+operation, operation, id, uint64(i))
			}
			commands := make([]foundation.CommandEnvelope, 8)
			for j := range commands {
				commands[j] = command
				// Transport retries may carry new request IDs but retain their key.
				commands[j].CommandID = fmt.Sprintf("duplicate-%s-request-%d", operation, j)
			}
			results := srhConcurrentCommands(t, fixtures, commands)
			for _, result := range results {
				require.Equal(t, http.StatusOK, result.status, result.body)
				require.Equal(t, srhRecoveryData(t, results[0].body), srhRecoveryData(t, result.body), "duplicate replay changed the result")
			}
			filters := int64(1)
			if operation == "create" {
				id = srhDecodeView(t, results[0].body).ID
			} else if operation == "delete" {
				filters = 0
			}
			srhRecoveryCounts(t, db, filters, int64(i+1), int64(i+1))
			srhRecoveryPrivateEvents(t, db)
		}
	})
}

// UseNumber is intentional: a float64 comparison would hide precision loss in
// both the expected and restored responses.
func srhRecoveryData(t *testing.T, body string) any {
	t.Helper()
	var envelope struct {
		Data any `json:"data"`
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&envelope))
	require.NotNil(t, envelope.Data)
	return envelope.Data
}

func TestSRHSnapshotRestartPreservesPrivateReceipts(t *testing.T) {
	testSRHSnapshotRestartPreservesPrivateReceipts(t, false)
}

func TestPostgresSRHSnapshotRestartPreservesPrivateReceipts(t *testing.T) {
	testSRHSnapshotRestartPreservesPrivateReceipts(t, true)
}

func testSRHSnapshotRestartPreservesPrivateReceipts(t *testing.T, postgres bool) {
	open := newModuleTestDB
	if postgres {
		open = postgresModuleDB
	}
	source := newSRHFixture(t, open(t, "SRH"))
	create := srhCommand("snapshot-create", "create")
	create.Input["name"] = "private-snapshot-original"
	create.Input["query"] = json.RawMessage(`{"integer":9007199254740993,"decimal":0.12345678901234567890123456789,"nested":{"private_marker":"private-query-value","items":[true,null,9007199254740995]}}`)
	createdBody := source.command(t, create, http.StatusOK)
	created := srhDecodeView(t, createdBody)
	require.EqualValues(t, 1, created.ObjectVersion)
	require.Contains(t, string(created.Query), "9007199254740993")
	require.Contains(t, string(created.Query), "0.12345678901234567890123456789")

	update := srhRecoveryMutation("snapshot-update", "update", created.ID, 1)
	update.Input["name"] = "private-snapshot-updated"
	update.Input["query"] = json.RawMessage(`{"integer":9007199254740997,"decimal":0.98765432109876543210987654321,"nested":{"private_marker":"private-query-value"}}`)
	updatedBody := source.command(t, update, http.StatusOK)
	updated := srhDecodeView(t, updatedBody)
	require.EqualValues(t, 2, updated.ObjectVersion)
	require.Contains(t, string(updated.Query), "9007199254740997")
	require.Contains(t, string(updated.Query), "0.98765432109876543210987654321")

	removedCreate := srhCommand("snapshot-removed-create", "create")
	removedCreatedBody := source.command(t, removedCreate, http.StatusOK)
	removed := srhDecodeView(t, removedCreatedBody)
	remove := srhRecoveryMutation("snapshot-delete", "delete", removed.ID, 1)
	removedBody := source.command(t, remove, http.StatusOK)
	srhRecoveryCounts(t, source.runtime.DB, 1, 4, 4)
	sourceEvents := srhRecoveryPrivateEvents(t, source.runtime.DB)
	var original domain.CustomFilter
	require.NoError(t, source.runtime.DB.First(&original, created.ID).Error)

	path := filepath.Join(t.TempDir(), "srh.snapshot.json")
	require.NoError(t, CreateSnapshot(source.runtime.DB, "SRH", "srh-private-v1", path))
	target := open(t, "SRH")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "SRH", "srh-private-v1", path, nil))

	replays := []struct {
		command foundation.CommandEnvelope
		body    string
	}{{create, createdBody}, {update, updatedBody}, {removedCreate, removedCreatedBody}, {remove, removedBody}}
	for restart := 0; restart < 2; restart++ {
		restored := newSRHFixture(t, target)
		restored.runtime.Config.RecoveryGeneration = "srh-private-v1"
		require.NoError(t, restored.runtime.Readiness())
		query := srhQuery()
		query.Filter = map[string]any{"id": created.ID}
		view := srhDecodeView(t, restored.query(t, query, http.StatusOK))
		require.EqualValues(t, 2, view.ObjectVersion)
		require.Equal(t, updated.Name, view.Name)
		require.Equal(t, updated.Query, view.Query)
		var row domain.CustomFilter
		require.NoError(t, target.First(&row, created.ID).Error)
		require.Equal(t, original.Query, row.Query)
		require.Equal(t, original.ObjectVersion, row.ObjectVersion)
		require.True(t, original.CreatedAt.Equal(row.CreatedAt))
		require.True(t, original.UpdatedAt.Equal(row.UpdatedAt))
		for _, replay := range replays {
			got := restored.command(t, replay.command, http.StatusOK)
			require.Equal(t, srhRecoveryData(t, replay.body), srhRecoveryData(t, got), "restart %d changed receipt %s", restart, replay.command.CommandID)
		}
		// Authorization is current TEN authority, including when restored receipts
		// contain successful results for an actor whose membership was revoked.
		restored.allowed.Store(false)
		for _, body := range []string{
			restored.command(t, create, http.StatusNotFound),
			restored.query(t, query, http.StatusNotFound),
		} {
			require.NotContains(t, body, "private-snapshot")
			require.NotContains(t, body, "private-query-value")
		}
		restored.allowed.Store(true)
		restored.outage.Store(true)
		body := restored.command(t, update, http.StatusServiceUnavailable)
		require.NotContains(t, body, "private-snapshot")
		require.NotContains(t, body, "private-query-value")
		restored.outage.Store(false)
		otherActor := create
		otherActor.Actor.ID = "8"
		restored.command(t, otherActor, http.StatusConflict)
		srhRecoveryCounts(t, target, 1, 4, 4)
		events := srhRecoveryPrivateEvents(t, target)
		for i, event := range events {
			require.Equal(t, sourceEvents[i].ChangeID, event.ChangeID)
			require.Equal(t, sourceEvents[i].AfterState, event.AfterState)
			require.Equal(t, sourceEvents[i].ObjectVersionAfter, event.ObjectVersionAfter)
		}
	}

	// Deleted identities must not be reused: otherwise a stale ID/version pair
	// could mutate an unrelated newly created filter after recovery.
	restarted := newSRHFixture(t, target)
	fresh := srhDecodeView(t, restarted.command(t, srhCommand("snapshot-fresh-create", "create"), http.StatusOK))
	require.Greater(t, fresh.ID, removed.ID)
	deletedStale := srhRecoveryMutation("snapshot-deleted-stale-update", "update", removed.ID, 1)
	restarted.command(t, deletedStale, http.StatusNotFound)
	advance := srhRecoveryMutation("snapshot-fresh-update", "update", created.ID, 2)
	advanced := srhDecodeView(t, restarted.command(t, advance, http.StatusOK))
	require.EqualValues(t, 3, advanced.ObjectVersion)
	stale := srhRecoveryMutation("snapshot-stale-update", "update", created.ID, 2)
	restarted.command(t, stale, http.StatusConflict)
	srhRecoveryCounts(t, target, 2, 6, 6)
	srhRecoveryPrivateEvents(t, target)
	for _, foreign := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.User{}, &domain.Contact{}, &domain.Conversation{}} {
		require.False(t, target.Migrator().HasTable(foreign), "SRH restore created foreign table %T", foreign)
	}
}

func TestSRHEmptySnapshotRetainsDeletedIdentity(t *testing.T) {
	testSRHEmptySnapshotRetainsDeletedIdentity(t, false)
}

func TestPostgresSRHEmptySnapshotRetainsDeletedIdentity(t *testing.T) {
	testSRHEmptySnapshotRetainsDeletedIdentity(t, true)
}

func testSRHEmptySnapshotRetainsDeletedIdentity(t *testing.T, postgres bool) {
	open := newModuleTestDB
	if postgres {
		open = postgresModuleDB
	}
	source := newSRHFixture(t, open(t, "SRH"))
	var deletedID uint
	for i := 0; i < 2; i++ {
		created := srhDecodeView(t, source.command(t, srhCommand(fmt.Sprintf("empty-create-%d", i), "create"), http.StatusOK))
		source.command(t, srhRecoveryMutation(fmt.Sprintf("empty-delete-%d", i), "delete", created.ID, 1), http.StatusOK)
		deletedID = created.ID
	}
	srhRecoveryCounts(t, source.runtime.DB, 0, 4, 4)
	path := filepath.Join(t.TempDir(), "srh-empty.snapshot.json")
	require.NoError(t, CreateSnapshot(source.runtime.DB, "SRH", "srh-empty-v1", path))
	target := open(t, "SRH")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "SRH", "srh-empty-v1", path, nil))
	restored := newSRHFixture(t, target)
	fresh := srhDecodeView(t, restored.command(t, srhCommand("empty-fresh-create", "create"), http.StatusOK))
	require.Greater(t, fresh.ID, deletedID, "empty saved-filter snapshot reused a deleted identity")
	restored.command(t, srhRecoveryMutation("empty-stale-update", "update", deletedID, 1), http.StatusNotFound)
	srhRecoveryCounts(t, target, 1, 5, 5)
	srhRecoveryPrivateEvents(t, target)
}
