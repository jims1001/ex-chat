package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Explicit local-test opt-in only. Every owner uses a separate disposable schema;
// all concurrent service instances use independent repository objects.
func TestChannelPostgresConcurrentIngress(t *testing.T) {
	dsn := os.Getenv("CHANNEL_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CHANNEL_TEST_POSTGRES_DSN is not configured")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	baseSQL, _ := base.DB()
	t.Cleanup(func() { baseSQL.Close() })
	dbs := map[string]*gorm.DB{}
	for _, owner := range []string{"CHN", "CUS", "CON", "MSG"} {
		schema := "channel_" + strings.ToLower(owner) + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
			t.Fatal(err)
		}
		db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		sqlDB.SetMaxOpenConns(6)
		t.Cleanup(func() {
			sqlDB.Close()
			if err := base.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
				t.Error(err)
			}
		})
		if err := database.AutoMigrateModule(db, owner); err != nil {
			t.Fatal(err)
		}
		dbs[owner] = db
	}
	f := channelFixture{s: newChannelTestService(dbs, &channelTestProvider{}), dbs: dbs}
	setupTwilio(t, f)
	run := func(raws [][]byte) {
		var wg sync.WaitGroup
		errs := make(chan error, len(raws))
		for _, raw := range raws {
			wg.Add(1)
			go func(raw []byte) {
				defer wg.Done()
				s := newChannelTestService(dbs, &channelTestProvider{})
				_, err := s.IngestTwilio(context.Background(), 91, channelTwilioSignature(91, raw), raw)
				errs <- err
			}(raw)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Error(err)
			}
		}
	}
	raw := channelTwilioForm("SM22222222222222222222222222222222", "same delivery")
	var retries [][]byte
	for i := 0; i < 20; i++ {
		retries = append(retries, raw)
	}
	run(retries)
	if t.Failed() {
		return
	}
	if n := channelCount(t, dbs["MSG"], &domain.Message{}); n != 1 {
		t.Fatal("duplicate messages", n)
	}
	if n := channelCount(t, dbs["CON"], &domain.Conversation{}); n != 1 {
		t.Fatal("duplicate conversations", n)
	}
	if n := channelCount(t, dbs["CUS"], &domain.Contact{}); n != 1 {
		t.Fatal("duplicate contacts", n)
	}
	var distinct [][]byte
	for i := 0; i < 12; i++ {
		raw := channelTwilioForm(fmt.Sprintf("SM%032x", i+100), "distinct delivery")
		raw = []byte(strings.Replace(string(raw), "14155550999", fmt.Sprintf("1415555%04d", i), 1))
		distinct = append(distinct, raw)
	}
	run(distinct)
	if t.Failed() {
		return
	}
	if n := channelCount(t, dbs["MSG"], &domain.Message{}); n != 13 {
		t.Fatal("lost messages", n)
	}
	var conversations []domain.Conversation
	if err := dbs["CON"].Find(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	ids := map[uint]bool{}
	for _, c := range conversations {
		if ids[c.DisplayID] {
			t.Fatal("duplicate display ID", c.DisplayID)
		}
		ids[c.DisplayID] = true
		if c.UnreadCount != 1 {
			t.Fatal("wrong unread count", c.UnreadCount)
		}
	}
	if len(conversations) != 13 {
		t.Fatal(len(conversations))
	}
}
