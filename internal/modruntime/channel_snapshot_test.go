package modruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestChannelOwnerSnapshotsPreserveCredentialsAndReplayKeys(t *testing.T) {
	fixtures := map[string]any{
		"CHN": &domain.ChannelConnection{ID: 10, AccountID: 91, InboxID: 11, Provider: "twilio", ProviderAccountID: "provider-account", RecipientID: "recipient", RecipientKey: "recipient-key", SecretCiphertext: "test-ciphertext-not-plaintext"},
		"CUS": &domain.ChannelContactIdentity{Key: "identity", AccountID: 91, InboxID: 11, ContactID: 12},
		"CON": &domain.ChannelConversationSession{Key: "session", AccountID: 91, InboxID: 11, ContactID: 12, ConversationID: 13},
		"MSG": &domain.ChannelMessageReceipt{Key: "receipt", AccountID: 91, MessageID: 14, Digest: "digest"},
	}
	for owner, model := range fixtures {
		t.Run(owner, func(t *testing.T) {
			open := func(name string) *gorm.DB {
				db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
				if err != nil {
					t.Fatal(err)
				}
				if err := database.AutoMigrateModule(db, owner); err != nil {
					t.Fatal(err)
				}
				return db
			}
			source, target := open("source"), open("target")
			if err := source.Create(model).Error; err != nil {
				t.Fatal(err)
			}
			snapshot := filepath.Join(t.TempDir(), "private-snapshot.json")
			if err := CreateSnapshot(source, owner, "channel-test", snapshot); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(snapshot)
			if info.Mode().Perm() != 0600 {
				t.Fatal("unsafe snapshot permissions")
			}
			if err := RestoreSnapshot(context.Background(), target, owner, "channel-test", snapshot, nil); err != nil {
				t.Fatal(err)
			}
			restored := reflect.New(reflect.TypeOf(model).Elem()).Interface()
			if err := target.First(restored).Error; err != nil {
				t.Fatal(err)
			}
			// Ignore database timestamp representation differences; compare persisted fields.
			want := reflect.ValueOf(model).Elem()
			got := reflect.ValueOf(restored).Elem()
			for i := 0; i < want.NumField(); i++ {
				name := want.Type().Field(i).Name
				if name == "CreatedAt" || name == "UpdatedAt" {
					continue
				}
				if !reflect.DeepEqual(want.Field(i).Interface(), got.Field(i).Interface()) {
					t.Fatalf("%s lost %s", owner, name)
				}
			}
			public, _ := json.Marshal(restored)
			if strings.Contains(string(public), "test-ciphertext") {
				t.Fatal("ciphertext leaked into public JSON")
			}
		})
	}
}
