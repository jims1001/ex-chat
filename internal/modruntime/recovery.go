package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type ModuleSnapshot struct {
	ModuleID   string                     `json:"module_id"`
	Generation string                     `json:"generation"`
	CreatedAt  time.Time                  `json:"created_at"`
	Tables     map[string]json.RawMessage `json:"tables"`
}

type snapshotFile struct {
	Checksum string         `json:"checksum"`
	Snapshot ModuleSnapshot `json:"snapshot"`
}

func modelTable(db *gorm.DB, model any) (string, error) {
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(model); err != nil {
		return "", err
	}
	return statement.Schema.Table, nil
}

func snapshotQuery(db *gorm.DB, model any) (*gorm.DB, error) {
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(model); err != nil {
		return nil, err
	}
	query := db.Model(model)
	for _, field := range statement.Schema.PrimaryFields {
		query = query.Order(field.DBName + " ASC")
	}
	return query, nil
}

func CreateSnapshot(db *gorm.DB, moduleID, generation, path string) error {
	if db == nil || strings.TrimSpace(generation) == "" || strings.TrimSpace(path) == "" {
		return errors.New("snapshot database, generation and path are required")
	}
	models, err := database.ModuleModels(moduleID)
	if err != nil {
		return err
	}
	snapshot := ModuleSnapshot{ModuleID: moduleID, Generation: generation, CreatedAt: time.Now().UTC(), Tables: map[string]json.RawMessage{}}
	for _, model := range models {
		table, err := modelTable(db, model)
		if err != nil {
			return err
		}
		slice := reflect.New(reflect.SliceOf(reflect.TypeOf(model).Elem()))
		query, err := snapshotQuery(db, model)
		if err != nil {
			return err
		}
		if err := query.Find(slice.Interface()).Error; err != nil {
			return fmt.Errorf("snapshot table %s: %w", table, err)
		}
		payload, err := json.Marshal(slice.Elem().Interface())
		if err != nil {
			return err
		}
		snapshot.Tables[table] = payload
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	filePayload, err := json.MarshalIndent(snapshotFile{Checksum: hex.EncodeToString(sum[:]), Snapshot: snapshot}, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, filePayload, 0600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func RestoreSnapshot(ctx context.Context, db *gorm.DB, moduleID, generation, path string, rebuild func(context.Context, *gorm.DB) error) error {
	if db == nil || strings.TrimSpace(generation) == "" {
		return errors.New("restore database and generation are required")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var file snapshotFile
	if err := json.Unmarshal(payload, &file); err != nil {
		return errors.New("module snapshot is invalid")
	}
	canonical, err := json.Marshal(file.Snapshot)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canonical)
	if file.Checksum == "" || !strings.EqualFold(file.Checksum, hex.EncodeToString(sum[:])) {
		return errors.New("module snapshot checksum mismatch")
	}
	if file.Snapshot.ModuleID != moduleID || file.Snapshot.Generation != generation {
		return errors.New("module snapshot identity mismatch")
	}
	models, err := database.ModuleModels(moduleID)
	if err != nil {
		return err
	}
	expected := make(map[string]any, len(models))
	for _, model := range models {
		table, err := modelTable(db, model)
		if err != nil {
			return err
		}
		expected[table] = model
	}
	if len(file.Snapshot.Tables) != len(expected) {
		return errors.New("module snapshot table set mismatch")
	}
	for table := range file.Snapshot.Tables {
		if _, ok := expected[table]; !ok {
			return errors.New("module snapshot contains a foreign table")
		}
	}
	tables := make([]string, 0, len(expected))
	for table := range expected {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, table := range tables {
			if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Table(table).Delete(expected[table]).Error; err != nil {
				return err
			}
		}
		for _, table := range tables {
			model := expected[table]
			slice := reflect.New(reflect.SliceOf(reflect.TypeOf(model).Elem()))
			if err := json.Unmarshal(file.Snapshot.Tables[table], slice.Interface()); err != nil {
				return fmt.Errorf("restore table %s: invalid rows", table)
			}
			if slice.Elem().Len() > 0 {
				if err := tx.Table(table).Create(slice.Interface()).Error; err != nil {
					return fmt.Errorf("restore table %s: %w", table, err)
				}
			}
		}
		if rebuild != nil {
			if err := rebuild(ctx, tx); err != nil {
				return err
			}
		}
		hash := sha256.Sum256([]byte(moduleID + ":" + generation))
		now := time.Now().UTC()
		marker := domain.LocalChangeJournal{ChangeID: "restore-" + hex.EncodeToString(hash[:])[:48], SourceModule: moduleID, ObjectType: "ModuleRecovery", ObjectDisplayID: generation, Action: "recovery_complete", ActorType: "System", SourceType: "module_restore", OccurredAt: now, RecordedAt: now, IngestedAt: now, CreatedAt: now, Result: "applied", Status: "delivered"}
		return tx.Create(&marker).Error
	})
}
