package service

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var campaignReadFailure = errors.New("private-campaign-storage-diagnostic")

type campaignPreflightFixture struct {
	ops, cus, con, msg *gorm.DB
	service            *CampaignService
	contacts           []domain.Contact
	label              domain.Label
	writes             int
}

func newCampaignPreflightFixture(t *testing.T, open func(*testing.T, string) *gorm.DB, count int) *campaignPreflightFixture {
	t.Helper()
	f := &campaignPreflightFixture{ops: open(t, "OPS"), cus: open(t, "CUS"), con: open(t, "CON"), msg: open(t, "MSG")}
	for name, db := range map[string]*gorm.DB{"OPS": f.ops, "CUS": f.cus, "CON": f.con, "MSG": f.msg} {
		require.NoError(t, database.AutoMigrateModule(db, name))
	}
	company := domain.Company{AccountID: 1, Name: "Preflight company"}
	require.NoError(t, f.cus.Create(&company).Error)
	f.label = domain.Label{AccountID: 1, Title: "vip-customer"}
	require.NoError(t, f.cus.Create(&f.label).Error)
	for i := 0; i < count; i++ {
		contact := domain.Contact{AccountID: 1, Name: fmt.Sprintf("Contact %03d", i), CompanyID: &company.ID}
		require.NoError(t, f.cus.Create(&contact).Error)
		require.NoError(t, f.cus.Create(&domain.ContactLabel{ContactID: contact.ID, LabelID: f.label.ID}).Error)
		f.contacts = append(f.contacts, contact)
	}
	require.NoError(t, f.cus.Create(&domain.Contact{AccountID: 2, Name: "Foreign contact"}).Error)
	conv := repository.NewConversationRepository(f.con)
	f.service = NewCampaignService(f.ops, conv, repository.NewMessageRepositoryWithConversationPort(f.msg, conv), repository.NewContactRepositoryWithOwnerPorts(f.cus, nil, nil, nil, nil, nil))
	f.service.SetAudienceOwner(repository.NewLabelRepositoryWithConversationPort(f.cus, conv))
	require.False(t, f.ops.Migrator().HasTable("contacts"))
	require.False(t, f.ops.Migrator().HasTable("conversations"))
	require.False(t, f.ops.Migrator().HasTable("messages"))
	require.False(t, f.cus.Migrator().HasTable("campaigns"))
	require.False(t, f.con.Migrator().HasTable("labels"))
	return f
}
func (f *campaignPreflightFixture) campaign(t *testing.T, kind, audience string) domain.Campaign {
	t.Helper()
	c := domain.Campaign{AccountID: 1, InboxID: 10, Title: "Prerequisite fixture", Message: "campaign message", CampaignType: kind, Status: "scheduled", Audience: audience}
	if kind == "ongoing" {
		c.Status = "active"
	}
	require.NoError(t, f.ops.Create(&c).Error)
	return c
}
func (f *campaignPreflightFixture) conversation(t *testing.T, contactID uint) domain.Conversation {
	t.Helper()
	c := domain.Conversation{AccountID: 1, InboxID: 10, ContactID: contactID, Status: domain.ConversationStatusOpen, DisplayID: contactID}
	require.NoError(t, f.con.Create(&c).Error)
	require.NoError(t, f.con.Create(&domain.ConversationLabel{ConversationID: c.ID, LabelID: f.label.ID}).Error)
	return c
}
func (f *campaignPreflightFixture) observe(t *testing.T) {
	t.Helper()
	for _, db := range []*gorm.DB{f.ops, f.cus, f.con, f.msg} {
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("campaign_effect_create", func(*gorm.DB) { f.writes++ }))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("campaign_effect_update", func(*gorm.DB) { f.writes++ }))
		require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("campaign_effect_delete", func(*gorm.DB) { f.writes++ }))
		t.Cleanup(func() {
			require.NoError(t, db.Callback().Create().Remove("campaign_effect_create"))
			require.NoError(t, db.Callback().Update().Remove("campaign_effect_update"))
			require.NoError(t, db.Callback().Delete().Remove("campaign_effect_delete"))
		})
	}
}
func campaignInjectQuery(t *testing.T, db *gorm.DB, table string, count bool, at int, after bool) func() int {
	t.Helper()
	calls := 0
	inject := func(tx *gorm.DB) {
		// CON failures in this fixture target the audience contact-ID Pluck,
		// never message activity's First(&Conversation) or target-existence
		// reads. This distinction also makes the old interleaved-send path
		// fail at its second audience read after its first real send.
		if table == "conversations" {
			if _, audienceIDs := tx.Statement.Dest.(*[]uint); !audienceIDs {
				return
			}
		}
		_, isCount := tx.Statement.Dest.(*int64)
		if tx.Statement.Table == table && isCount == count {
			calls++
			if calls == at {
				if after {
					require.Positive(t, tx.RowsAffected, "rows+error fixture must have rows")
				}
				_ = tx.AddError(campaignReadFailure)
			}
		}
	}
	if after {
		require.NoError(t, db.Callback().Query().After("gorm:after_query").Register("campaign_read_failure", inject))
	} else {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("campaign_read_failure", inject))
	}
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("campaign_read_failure")) })
	return func() int { return calls }
}
func (f *campaignPreflightFixture) assertNoEffects(t *testing.T, c domain.Campaign, count int, err error) {
	t.Helper()
	require.Zero(t, count)
	require.ErrorIs(t, err, ErrCampaignPrerequisiteRead)
	require.ErrorIs(t, err, campaignReadFailure)
	require.NotContains(t, err.Error(), campaignReadFailure.Error())
	require.Zero(t, f.writes, "preflight attempted a write")
	var stored domain.Campaign
	require.NoError(t, f.ops.First(&stored, c.ID).Error)
	require.Equal(t, c.Status, stored.Status)
	var messages, deliveries int64
	require.NoError(t, f.msg.Model(&domain.Message{}).Count(&messages).Error)
	require.NoError(t, f.ops.Model(&domain.CampaignDelivery{}).Count(&deliveries).Error)
	require.Zero(t, messages)
	require.Zero(t, deliveries)
}

func TestCampaignPrerequisiteOwners(t *testing.T) {
	runCampaignPrerequisiteOwners(t, func(t *testing.T, owner string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		conn, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		return db
	})
}

func runCampaignPrerequisiteOwners(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("bulk_contact_read_failures", func(t *testing.T) {
		for _, tc := range []struct {
			name, table string
			count       bool
			at          int
			after       bool
		}{
			{"first_count", "contacts", true, 1, false}, {"late_count", "contacts", true, 2, false},
			{"first_query", "contacts", false, 1, false}, {"late_query", "contacts", false, 2, false},
			{"first_rows_error", "contacts", false, 1, true}, {"late_rows_error", "contacts", false, 2, true},
			{"first_company_preload", "companies", false, 1, false}, {"late_company_preload", "companies", false, 2, false},
			{"first_label_relation_preload", "contact_labels", false, 1, false}, {"late_label_relation_preload", "contact_labels", false, 2, false},
			{"first_label_preload", "labels", false, 1, false}, {"late_label_preload", "labels", false, 2, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newCampaignPreflightFixture(t, open, 105)
				c := f.campaign(t, "one_off", "all")
				f.observe(t)
				calls := campaignInjectQuery(t, f.cus, tc.table, tc.count, tc.at, tc.after)
				n, err := f.service.TriggerCampaign(1, c.ID)
				f.assertNoEffects(t, c, n, err)
				require.Equal(t, tc.at, calls())
			})
		}
	})
	t.Run("bulk_complete_pagination_and_order", func(t *testing.T) {
		f := newCampaignPreflightFixture(t, open, 105)
		c := f.campaign(t, "one_off", "all")
		// One-off campaigns must not acquire new ongoing dedup semantics.
		calls := campaignInjectQuery(t, f.ops, "campaign_deliveries", true, 1, false)
		n, err := f.service.TriggerCampaign(1, c.ID)
		require.NoError(t, err)
		require.Equal(t, 105, n)
		require.Zero(t, calls())
		require.NoError(t, f.ops.Callback().Query().Remove("campaign_read_failure"))
		var deliveries []domain.CampaignDelivery
		require.NoError(t, f.ops.Order("id").Find(&deliveries).Error)
		require.Len(t, deliveries, 105)
		for i, d := range deliveries {
			require.Equal(t, f.contacts[104-i].ID, d.ContactID)
			require.Equal(t, uint(1), d.AccountID)
		}
		var stored domain.Campaign
		require.NoError(t, f.ops.First(&stored, c.ID).Error)
		require.Equal(t, "completed", stored.Status)
	})
	t.Run("bulk_dedup_read_failures", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			at    int
			after bool
		}{{"first", 1, false}, {"later", 3, false}, {"later_count_error", 3, true}} {
			t.Run(tc.name, func(t *testing.T) {
				f := newCampaignPreflightFixture(t, open, 3)
				c := f.campaign(t, "ongoing", "all")
				f.observe(t)
				calls := campaignInjectQuery(t, f.ops, "campaign_deliveries", true, tc.at, tc.after)
				n, err := f.service.TriggerCampaign(1, c.ID)
				// Remove the injection before querying the delivery table in assertions.
				require.Equal(t, tc.at, calls())
				require.NoError(t, f.ops.Callback().Query().Remove("campaign_read_failure"))
				f.assertNoEffects(t, c, n, err)
			})
		}
	})
	t.Run("bulk_audience_read_failures", func(t *testing.T) {
		for _, tc := range []struct {
			name, audience, owner, table string
			after                        bool
		}{
			{"exact_labels", `{"labels":["vip-customer"]}`, "CUS", "labels", false},
			{"fuzzy_labels", "vip", "CUS", "labels", true},
			{"conversation_labels", `{"labels":["vip-customer"]}`, "CON", "conversations", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newCampaignPreflightFixture(t, open, 3)
				f.conversation(t, f.contacts[0].ID)
				c := f.campaign(t, "ongoing", tc.audience)
				f.observe(t)
				db := f.con
				at := 1
				if tc.owner == "CUS" {
					db = f.cus
					at = 2
				} // contact label preload precedes audience lookup
				calls := campaignInjectQuery(t, db, tc.table, false, at, tc.after)
				n, err := f.service.TriggerCampaign(1, c.ID)
				require.Equal(t, at, calls())
				f.assertNoEffects(t, c, n, err)
			})
		}
	})
	t.Run("ongoing_contact_read_failures", func(t *testing.T) {
		for _, tc := range []struct {
			name, owner, table string
			after              bool
		}{
			{"campaign_list", "OPS", "campaigns", false}, {"campaign_rows_error", "OPS", "campaigns", true},
			{"contact_query", "CUS", "contacts", false}, {"contact_rows_error", "CUS", "contacts", true},
			{"company_preload", "CUS", "companies", false}, {"labels_preload", "CUS", "labels", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newCampaignPreflightFixture(t, open, 1)
				c := f.campaign(t, "ongoing", "all")
				conv := f.conversation(t, f.contacts[0].ID)
				f.observe(t)
				db := f.cus
				if tc.owner == "OPS" {
					db = f.ops
				}
				calls := campaignInjectQuery(t, db, tc.table, false, 1, tc.after)
				n, err := f.service.TriggerOngoingCampaignForContact(1, 10, f.contacts[0].ID, conv.ID)
				require.Equal(t, 1, calls())
				require.NoError(t, db.Callback().Query().Remove("campaign_read_failure"))
				f.assertNoEffects(t, c, n, err)
			})
		}
	})
	t.Run("ongoing_later_campaign_failure", func(t *testing.T) {
		for _, tc := range []struct {
			name, owner, table string
			count              bool
			after              bool
		}{
			{"dedup", "OPS", "campaign_deliveries", true, false}, {"dedup_rows_error", "OPS", "campaign_deliveries", true, true},
			{"labels", "CUS", "labels", false, false}, {"audience_rows_error", "CON", "conversations", false, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newCampaignPreflightFixture(t, open, 1)
				// Both campaigns require the same healthy audience reads, so the
				// injected second-campaign failure is independent of SQL row order.
				first := f.campaign(t, "ongoing", `{"labels":["vip-customer"]}`)
				f.campaign(t, "ongoing", `{"labels":["vip-customer"]}`)
				conv := f.conversation(t, f.contacts[0].ID)
				f.observe(t)
				db := f.ops
				at := 2
				if tc.owner == "CUS" {
					db = f.cus
					at = 3 // Contact preload, first audience, second audience.
				}
				if tc.owner == "CON" {
					db = f.con
					at = 2 // First audience succeeds before the second fails.
				}
				calls := campaignInjectQuery(t, db, tc.table, tc.count, at, tc.after)
				n, err := f.service.TriggerOngoingCampaignForContact(1, 10, f.contacts[0].ID, conv.ID)
				require.Equal(t, at, calls())
				require.NoError(t, db.Callback().Query().Remove("campaign_read_failure"))
				f.assertNoEffects(t, first, n, err)
			})
		}
	})
	t.Run("ongoing_healthy_order_and_scope", func(t *testing.T) {
		f := newCampaignPreflightFixture(t, open, 1)
		contact := f.contacts[0]
		conv := f.conversation(t, contact.ID)
		first := f.campaign(t, "ongoing", "all")
		first.Message = "first"
		require.NoError(t, f.ops.Save(&first).Error)
		delivered := f.campaign(t, "ongoing", "all")
		require.NoError(t, f.ops.Create(&domain.CampaignDelivery{AccountID: 1, CampaignID: delivered.ID, ContactID: contact.ID, ConversationID: conv.ID, Status: "sent"}).Error)
		f.campaign(t, "ongoing", `{"contact_ids":[999999]}`)
		last := f.campaign(t, "ongoing", `{"labels":["vip-customer"]}`)
		last.Message = "last"
		require.NoError(t, f.ops.Save(&last).Error)
		for _, c := range []domain.Campaign{
			{AccountID: 2, InboxID: 10, CampaignType: "ongoing", Status: "active", Message: "foreign"},
			{AccountID: 1, InboxID: 20, CampaignType: "ongoing", Status: "active", Message: "other inbox"},
			{AccountID: 1, InboxID: 10, CampaignType: "ongoing", Status: "paused", Message: "paused"},
			{AccountID: 1, InboxID: 10, CampaignType: "one_off", Status: "active", Message: "one off"},
		} {
			require.NoError(t, f.ops.Create(&c).Error)
		}
		// Foreign delivery does not suppress this account's eligible campaign.
		require.NoError(t, f.ops.Create(&domain.CampaignDelivery{AccountID: 2, CampaignID: first.ID, ContactID: contact.ID, Status: "sent"}).Error)
		// ListActiveOngoing intentionally has no ORDER BY. Observe the exact
		// owner-returned order for this invocation rather than assume ID order.
		var returnedOrder []domain.Campaign
		require.NoError(t, f.ops.Callback().Query().After("gorm:after_query").Register("campaign_returned_order", func(tx *gorm.DB) {
			if tx.Statement.Table == "campaigns" && returnedOrder == nil {
				if campaigns, ok := tx.Statement.Dest.(*[]domain.Campaign); ok {
					returnedOrder = append([]domain.Campaign(nil), (*campaigns)...)
				}
			}
		}))
		t.Cleanup(func() { require.NoError(t, f.ops.Callback().Query().Remove("campaign_returned_order")) })
		n, err := f.service.TriggerOngoingCampaignForContact(1, 10, contact.ID, conv.ID)
		require.NoError(t, err)
		require.Equal(t, 2, n)
		var messages []domain.Message
		require.NoError(t, f.msg.Order("id").Find(&messages).Error)
		require.Len(t, messages, 2)
		require.Len(t, returnedOrder, 4)
		var expected, actual []string
		for _, campaign := range returnedOrder {
			if campaign.ID == first.ID || campaign.ID == last.ID {
				expected = append(expected, campaign.Message)
			}
		}
		for _, message := range messages {
			actual = append(actual, message.Content)
		}
		require.Equal(t, expected, actual)
		n, err = f.service.TriggerOngoingCampaignForContact(1, 10, contact.ID, conv.ID)
		require.NoError(t, err)
		require.Zero(t, n)
	})
	t.Run("healthy_empty_and_status_contracts", func(t *testing.T) {
		f := newCampaignPreflightFixture(t, open, 1)
		for _, ids := range [][4]uint{{1, 0, 1, 1}, {1, 10, 0, 1}, {1, 10, f.contacts[0].ID, 1}} {
			n, err := f.service.TriggerOngoingCampaignForContact(ids[0], ids[1], ids[2], ids[3])
			require.NoError(t, err)
			require.Zero(t, n)
		}
		c := f.campaign(t, "ongoing", "all")
		n, err := f.service.TriggerOngoingCampaignForContact(1, 10, 999999, 1)
		require.NoError(t, err)
		require.Zero(t, n)
		n, err = f.service.TriggerOngoingCampaignForContact(2, 10, f.contacts[0].ID, 1)
		require.NoError(t, err)
		require.Zero(t, n)
		c.Status = "scheduled"
		require.NoError(t, f.ops.Save(&c).Error)
		require.NoError(t, f.ops.Create(&domain.CampaignDelivery{AccountID: 1, CampaignID: c.ID, ContactID: f.contacts[0].ID, Status: "sent"}).Error)
		n, err = f.service.TriggerCampaign(1, c.ID)
		require.NoError(t, err)
		require.Zero(t, n)
		require.NoError(t, f.ops.First(&c, c.ID).Error)
		require.Equal(t, "active", c.Status)
		empty := f.campaign(t, "one_off", `{"contact_ids":[999999]}`)
		n, err = f.service.TriggerCampaign(1, empty.ID)
		require.NoError(t, err)
		require.Zero(t, n)
		require.NoError(t, f.ops.First(&empty, empty.ID).Error)
		require.Equal(t, "scheduled", empty.Status)
		empty.AccountID = 3
		require.NoError(t, f.ops.Save(&empty).Error)
		n, err = f.service.TriggerCampaign(3, empty.ID)
		require.EqualError(t, err, "no contacts found in account audience")
		require.Zero(t, n)
		require.NotErrorIs(t, err, ErrCampaignPrerequisiteRead)
	})
	t.Run("bulk_selector_parity", func(t *testing.T) {
		for _, tc := range []struct {
			name, audience string
			want           int
		}{
			{"all", "all", 2}, {"empty", "", 2}, {"mixed_ids", `{"contact_ids":[1,"2"]}`, 2},
			{"typed_string_ids", `{"type":"contact_ids","values":["1"]}`, 1},
			{"typed_numeric_ids", `{"type":"contact_ids","values":[1]}`, 0},
			{"exact_labels", `{"labels":["vip-customer"]}`, 1}, {"fuzzy_label", "vip", 1},
			{"unknown_json", `{"unknown":"vip"}`, 0}, {"whitespace_all", " all ", 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newCampaignPreflightFixture(t, open, 2)
				f.conversation(t, f.contacts[0].ID)
				c := f.campaign(t, "one_off", tc.audience)
				n, err := f.service.TriggerCampaign(1, c.ID)
				require.NoError(t, err)
				require.Equal(t, tc.want, n)
			})
		}
	})
	t.Run("per_contact_selector_parity", func(t *testing.T) {
		for _, tc := range []struct {
			name, audience string
			want           int
		}{
			{"all", "all", 1}, {"whitespace_all", " all ", 1}, {"numeric_id", `{"contact_ids":[1]}`, 1}, {"string_id", `{"contact_ids":["1"]}`, 1},
			{"typed_ids_remain_label_fallback", `{"type":"contact_ids","values":["1"]}`, 0},
			{"exact_labels", `{"labels":["vip-customer"]}`, 1}, {"fuzzy_label", "vip", 1}, {"unknown_json", `{"unknown":"vip"}`, 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newCampaignPreflightFixture(t, open, 1)
				conv := f.conversation(t, f.contacts[0].ID)
				f.campaign(t, "ongoing", tc.audience)
				n, err := f.service.TriggerOngoingCampaignForContact(1, 10, f.contacts[0].ID, conv.ID)
				require.NoError(t, err)
				require.Equal(t, tc.want, n)
			})
		}
	})
	t.Run("business_rejections_are_separate", func(t *testing.T) {
		f := newCampaignPreflightFixture(t, open, 1)
		c := f.campaign(t, "one_off", "all")
		f.observe(t)
		for _, status := range []string{"completed", "cancelled", "paused"} {
			require.NoError(t, f.ops.Model(&c).Update("status", status).Error)
			f.writes = 0
			n, err := f.service.TriggerCampaign(1, c.ID)
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrCampaignPrerequisiteRead)
			require.Zero(t, n)
			require.Zero(t, f.writes)
		}
		_, err := f.service.TriggerCampaign(2, c.ID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		require.NotErrorIs(t, err, ErrCampaignPrerequisiteRead)
	})
	t.Run("bulk_campaign_read_error", func(t *testing.T) {
		f := newCampaignPreflightFixture(t, open, 1)
		c := f.campaign(t, "one_off", "all")
		f.observe(t)
		calls := campaignInjectQuery(t, f.ops, "campaigns", false, 1, true)
		n, err := f.service.TriggerCampaign(1, c.ID)
		require.Equal(t, 1, calls())
		require.NoError(t, f.ops.Callback().Query().Remove("campaign_read_failure"))
		f.assertNoEffects(t, c, n, err)
	})
	t.Run("downstream_partial_effects_unchanged", func(t *testing.T) {
		f := newCampaignPreflightFixture(t, open, 2)
		c := f.campaign(t, "one_off", "all")
		lookups := 0
		require.NoError(t, f.con.Callback().Query().Before("gorm:query").Register("campaign_downstream_lookup", func(tx *gorm.DB) {
			if tx.Statement.Table == "conversations" && strings.Contains(fmt.Sprint(tx.Statement.Clauses["WHERE"]), "contact_id") {
				lookups++
				if lookups == 2 {
					_ = tx.AddError(campaignReadFailure)
				}
			}
		}))
		t.Cleanup(func() { require.NoError(t, f.con.Callback().Query().Remove("campaign_downstream_lookup")) })
		n, err := f.service.TriggerCampaign(1, c.ID)
		require.ErrorIs(t, err, campaignReadFailure)
		require.NotErrorIs(t, err, ErrCampaignPrerequisiteRead)
		require.Equal(t, 1, n)
		require.Equal(t, 2, lookups)
		var messages int64
		require.NoError(t, f.msg.Model(&domain.Message{}).Count(&messages).Error)
		require.Equal(t, int64(1), messages)
	})
	t.Run("already_delivered_skips_unneeded_audience", func(t *testing.T) {
		f := newCampaignPreflightFixture(t, open, 1)
		c := f.campaign(t, "ongoing", `{"labels":["vip-customer"]}`)
		conv := f.conversation(t, f.contacts[0].ID)
		require.NoError(t, f.ops.Create(&domain.CampaignDelivery{AccountID: 1, CampaignID: c.ID, ContactID: f.contacts[0].ID, Status: "sent"}).Error)
		calls := campaignInjectQuery(t, f.con, "conversations", false, 1, false)
		n, err := f.service.TriggerOngoingCampaignForContact(1, 10, f.contacts[0].ID, conv.ID)
		require.NoError(t, err)
		require.Zero(t, n)
		require.Zero(t, calls())
	})
}
