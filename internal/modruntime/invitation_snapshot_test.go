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
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

func invitationSnapshotDigest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func invitationSnapshotFixtures() (domain.AgentInvitation, domain.InvitationIdentity) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	invite := domain.AgentInvitation{ID: 17, AccountID: 91, Email: "invitee@example.test", Role: domain.RoleAgent,
		InviterID: 2, InviterMembershipID: 3, TokenDigest: invitationSnapshotDigest("mailed-proof"), DeliveryState: "submitted",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	identity := domain.InvitationIdentity{ID: 23, OperationKey: invitationSnapshotDigest("operation"), UserID: 41, Email: invite.Email, CreatedAt: now}
	return invite, identity
}

// PostgreSQL timestamptz preserves the instant but pgx scans binary timestamps
// into time.Local by default. Compare every private field with canonical UTC
// locations, without truncating precision or ignoring any persisted value.
func invitationSnapshotRowsForComparison(t *testing.T, rows any) []byte {
	t.Helper()
	stamp := func(value *time.Time) *time.Time {
		if value == nil {
			return nil
		}
		utc := value.UTC()
		return &utc
	}
	switch values := rows.(type) {
	case []domain.AgentInvitation:
		copied := append([]domain.AgentInvitation{}, values...)
		for i := range copied {
			row := &copied[i]
			row.CreatedAt, row.UpdatedAt, row.ExpiresAt = row.CreatedAt.UTC(), row.UpdatedAt.UTC(), row.ExpiresAt.UTC()
			row.ConsumedAt, row.RevokedAt = stamp(row.ConsumedAt), stamp(row.RevokedAt)
		}
		rows = copied
	case []domain.InvitationIdentity:
		copied := append([]domain.InvitationIdentity{}, values...)
		for i := range copied {
			copied[i].CreatedAt = copied[i].CreatedAt.UTC()
		}
		rows = copied
	default:
		t.Fatalf("unsupported invitation comparison type: %T", rows)
	}
	raw, err := marshalSnapshotRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Exercise the actual pgx timestamptz codec without opening a database. This
// proves location-only differences are harmless while even a one-nanosecond
// instant change and hidden-field changes remain observable to the assertions.
func TestInvitationSnapshotComparisonPreservesInstantsAndPrivateFields(t *testing.T) {
	invite, identity := invitationSnapshotFixtures()
	codec := pgtype.NewMap()
	raw, err := codec.Encode(pgtype.TimestamptzOID, pgtype.BinaryFormatCode, invite.CreatedAt, nil)
	if err != nil {
		t.Fatal(err)
	}
	var scanned time.Time
	if err := codec.Scan(pgtype.TimestamptzOID, pgtype.BinaryFormatCode, raw, &scanned); err != nil {
		t.Fatal(err)
	}
	if !scanned.Equal(invite.CreatedAt) {
		t.Fatal("pgx codec changed timestamp instant")
	}
	t.Logf("pgx timestamp scan: fixture=%s scanned=%s same_instant=%t same_representation=%t", invite.CreatedAt.Format(time.RFC3339Nano), scanned.Format(time.RFC3339Nano), scanned.Equal(invite.CreatedAt), reflect.DeepEqual(scanned, invite.CreatedAt))
	local := time.FixedZone("test-offset", 4*60*60)
	consumed, revoked := invite.CreatedAt.Add(time.Minute), invite.ExpiresAt
	user, member := uint(41), uint(42)
	invite.ConsumedAt, invite.RevokedAt, invite.AcceptedUserID, invite.AcceptedMembershipID = &consumed, &revoked, &user, &member
	want := invitationSnapshotRowsForComparison(t, []domain.AgentInvitation{invite})
	actual := invite
	actual.CreatedAt, actual.UpdatedAt, actual.ExpiresAt = scanned, invite.UpdatedAt.In(local), invite.ExpiresAt.In(local)
	localConsumed, localRevoked := consumed.In(local), revoked.In(local)
	actual.ConsumedAt, actual.RevokedAt = &localConsumed, &localRevoked
	if string(want) != string(invitationSnapshotRowsForComparison(t, []domain.AgentInvitation{actual})) {
		t.Fatal("time location changed invitation comparison")
	}
	actual.CreatedAt = actual.CreatedAt.Add(time.Nanosecond)
	if string(want) == string(invitationSnapshotRowsForComparison(t, []domain.AgentInvitation{actual})) {
		t.Fatal("comparison ignored changed timestamp instant")
	}
	actual = invite
	actual.TokenDigest = invitationSnapshotDigest("different-proof")
	if string(want) == string(invitationSnapshotRowsForComparison(t, []domain.AgentInvitation{actual})) {
		t.Fatal("comparison ignored hidden digest change")
	}
	actual = invite
	otherMember := member + 1
	actual.AcceptedMembershipID = &otherMember
	if string(want) == string(invitationSnapshotRowsForComparison(t, []domain.AgentInvitation{actual})) {
		t.Fatal("comparison ignored hidden membership change")
	}
	actualIdentity := identity
	actualIdentity.CreatedAt = scanned
	if string(invitationSnapshotRowsForComparison(t, []domain.InvitationIdentity{identity})) != string(invitationSnapshotRowsForComparison(t, []domain.InvitationIdentity{actualIdentity})) {
		t.Fatal("time location changed identity comparison")
	}
}

func TestInvitationSnapshotPrivateCodecsRoundTripAndCoverModelFields(t *testing.T) {
	invite, identity := invitationSnapshotFixtures()
	for _, pair := range []struct{ model, codec any }{
		{domain.AgentInvitation{}, agentInvitationSnapshot{}}, {domain.InvitationIdentity{}, invitationIdentitySnapshot{}},
	} {
		model, codec := reflect.TypeOf(pair.model), reflect.TypeOf(pair.codec)
		if model.NumField() != codec.NumField() {
			t.Fatal("private invitation field inventory drifted")
		}
		for i := 0; i < model.NumField(); i++ {
			field := model.Field(i)
			private, ok := codec.FieldByName(field.Name)
			if !ok || private.Type != field.Type || private.Tag.Get("json") == "" || private.Tag.Get("json") == "-" || strings.Contains(private.Tag.Get("json"), "omitempty") {
				t.Fatalf("private invitation field not covered: %s", field.Name)
			}
		}
	}
	for _, state := range []string{"pending_delivery", "submitted", "captured_mock", "delivery_unknown"} {
		for _, terminal := range []string{"pending", "consumed", "revoked"} {
			row := invite
			row.DeliveryState = state
			custom, intended, acceptedMembership := uint(7), uint(41), uint(42)
			row.CustomRoleID, row.RoleFingerprint, row.IntendedUserID = &custom, invitationSnapshotDigest("role"), &intended
			stamp := row.CreatedAt.Add(time.Minute)
			if terminal == "consumed" {
				row.ConsumedAt, row.AcceptedUserID, row.AcceptedMembershipID = &stamp, &intended, &acceptedMembership
			}
			if terminal == "revoked" {
				row.RevokedAt = &stamp
			}
			raw, err := marshalSnapshotRows([]domain.AgentInvitation{row})
			var restored []domain.AgentInvitation
			if err != nil || unmarshalSnapshotRows(raw, &restored) != nil || !reflect.DeepEqual(restored, []domain.AgentInvitation{row}) {
				t.Fatal("invitation private round trip failed")
			}
			public, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(public, &fields); err != nil {
				t.Fatal(err)
			}
			model, codec := reflect.TypeOf(row), reflect.TypeOf(agentInvitationSnapshot{})
			for i := 0; i < model.NumField(); i++ {
				if model.Field(i).Tag.Get("json") == "-" {
					if _, exists := fields[codec.Field(i).Tag.Get("json")]; exists {
						t.Fatalf("public invitation exposed %s", model.Field(i).Name)
					}
				}
			}
		}
	}
	raw, err := marshalSnapshotRows([]domain.InvitationIdentity{identity})
	var restored []domain.InvitationIdentity
	if err != nil || unmarshalSnapshotRows(raw, &restored) != nil || !reflect.DeepEqual(restored, []domain.InvitationIdentity{identity}) {
		t.Fatal("identity private round trip failed")
	}
	public, err := json.Marshal(identity)
	if err != nil || string(public) != "{}" {
		t.Fatal("public activation checkpoint exposed private fields")
	}
}

func TestInvitationSnapshotCodecsRejectAmbiguousAndIncompleteRows(t *testing.T) {
	invite, identity := invitationSnapshotFixtures()
	for _, fixture := range []struct {
		name, codec string
		rows        any
		target      func() any
		nullable    map[string]bool
	}{
		{"invitations", agentInvitationSnapshotCodec, []domain.AgentInvitation{invite}, func() any { return &[]domain.AgentInvitation{invite} }, map[string]bool{"custom_role_id": true, "intended_user_id": true, "consumed_at": true, "accepted_user_id": true, "accepted_membership_id": true, "revoked_at": true}},
		{"identities", invitationIdentitySnapshotCodec, []domain.InvitationIdentity{identity}, func() any { return &[]domain.InvitationIdentity{identity} }, nil},
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
				"unknown_codec":       []byte(`{"codec":"invitation.future.v99","rows":[]}`),
				"null_rows":           []byte(`{"codec":"` + fixture.codec + `","rows":null}`),
				"missing_rows":        []byte(`{"codec":"` + fixture.codec + `"}`),
				"case_alias_table":    []byte(strings.Replace(string(raw), `"codec":`, `"Codec":`, 1)),
				"duplicate_table_key": []byte(strings.Replace(string(raw), `"codec":`, `"codec":"`+fixture.codec+`","codec":`, 1)),
				"duplicate_row_key":   []byte(strings.Replace(string(raw), `"id":`, `"id":1,"id":`, 1)),
				"case_alias_row":      []byte(strings.Replace(string(raw), `"id":`, `"ID":`, 1)),
				"trailing":            append(append([]byte{}, raw...), []byte(` {}`)...),
			}
			for field, old := range rows[0] {
				delete(rows[0], field)
				table["rows"], _ = json.Marshal(rows)
				bad["missing_"+field], _ = json.Marshal(table)
				if !fixture.nullable[field] {
					rows[0][field] = json.RawMessage(`null`)
					table["rows"], _ = json.Marshal(rows)
					bad["null_"+field], _ = json.Marshal(table)
				}
				rows[0][field] = old
			}
			rows[0]["future_secret"] = json.RawMessage(`"forbidden"`)
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
						t.Fatal("invalid invitation snapshot accepted")
					}
					after, _ := marshalSnapshotRows(reflect.ValueOf(target).Elem().Interface())
					if string(before) != string(after) {
						t.Fatal("failed decoder changed target")
					}
				})
			}
		})
	}
	for _, rows := range []any{[]domain.AgentInvitation{}, []domain.InvitationIdentity{}, []domain.AgentInvitation(nil), []domain.InvitationIdentity(nil)} {
		raw, err := marshalSnapshotRows(rows)
		target := reflect.New(reflect.TypeOf(rows)).Interface()
		if err != nil || unmarshalSnapshotRows(raw, target) != nil || reflect.ValueOf(target).Elem().IsNil() {
			t.Fatal("empty invitation snapshot failed")
		}
	}
}

func TestInvitationOwnerSnapshotInvalidatesPendingAndPreservesFacts(t *testing.T) {
	testInvitationOwnerSnapshot(t, false)
}
func TestPostgresInvitationOwnerSnapshotInvalidatesPendingAndPreservesFacts(t *testing.T) {
	testInvitationOwnerSnapshot(t, true)
}
func testInvitationOwnerSnapshot(t *testing.T, postgres bool) {
	open := newModuleTestDB
	if postgres {
		open = postgresModuleDB
	}
	source, target := open(t, "TEN"), open(t, "TEN")
	invite, _ := invitationSnapshotFixtures()
	rows := []domain.AgentInvitation{}
	for i, state := range []string{"pending_delivery", "submitted", "captured_mock", "delivery_unknown"} {
		row := invite
		row.ID += uint(i)
		row.Email = state + "@example.test"
		row.TokenDigest = invitationSnapshotDigest(state)
		row.DeliveryState = state
		rows = append(rows, row)
	}
	stamp, userID, membershipID := invite.CreatedAt.Add(time.Minute), uint(41), uint(42)
	consumed, revoked := invite, invite
	consumed.ID, consumed.Email, consumed.TokenDigest = 101, "consumed@example.test", invitationSnapshotDigest("consumed")
	consumed.ConsumedAt, consumed.AcceptedUserID, consumed.AcceptedMembershipID = &stamp, &userID, &membershipID
	revoked.ID, revoked.Email, revoked.TokenDigest, revoked.RevokedAt = 102, "revoked@example.test", invitationSnapshotDigest("revoked"), &stamp
	rows = append(rows, consumed, revoked)
	account := domain.Account{ID: invite.AccountID, Name: "Inviting tenant"}
	if err := source.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	members := []domain.AccountUser{
		{ID: invite.InviterMembershipID, AccountID: invite.AccountID, UserID: invite.InviterID, Role: domain.RoleAdministrator},
		{ID: membershipID, AccountID: invite.AccountID, UserID: userID, Role: domain.RoleAgent},
	}
	if err := source.Create(&members).Error; err != nil {
		t.Fatal(err)
	}
	tokens := make([]string, len(rows))
	for i := range rows {
		tokens[i] = invitationSnapshotDigest(rows[i].Email + "-mailed-token")
		digest, err := repository.AgentInvitationTokenDigest(tokens[i])
		if err != nil {
			t.Fatal(err)
		}
		rows[i].TokenDigest = digest
	}
	if err := source.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if row.RevokedAt == nil && (row.DeliveryState == "submitted" || row.DeliveryState == "captured_mock") {
			if _, err := repository.NewAccountRepository(source).InspectAgentInvitation(tokens[i]); err != nil {
				t.Fatalf("source proof should be usable before restore: %v", err)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "ten.json")
	if err := CreateSnapshot(source, "TEN", "invitation-restore", path); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("invitation backup must be private")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := RestoreSnapshot(context.Background(), target, "TEN", "invitation-restore", path, nil); err != nil {
			t.Fatal(err)
		}
		var restored []domain.AgentInvitation
		if err := target.Order("id ASC").Find(&restored).Error; err != nil || len(restored) != len(rows) {
			t.Fatal("invitation rows missing after restore")
		}
		for i, row := range restored {
			want := rows[i]
			if want.RevokedAt == nil {
				expiry := want.ExpiresAt
				want.RevokedAt = &expiry
			}
			gotJSON := invitationSnapshotRowsForComparison(t, []domain.AgentInvitation{row})
			wantJSON := invitationSnapshotRowsForComparison(t, []domain.AgentInvitation{want})
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("restored invitation differs or remains pending: %d", row.ID)
			}
		}
		var pending int64
		if err := target.Model(&domain.AgentInvitation{}).Where("consumed_at IS NULL AND revoked_at IS NULL").Count(&pending).Error; err != nil || pending != 0 {
			t.Fatal("backup restored live invitation proof")
		}
		for i, token := range tokens {
			if _, err := repository.NewAccountRepository(target).InspectAgentInvitation(token); !errors.Is(err, repository.ErrInvitationInvalid) {
				t.Fatalf("restored proof remained inspectable: %v", err)
			}
			if _, err := repository.NewAccountRepository(target).AcceptAgentInvitation(token, userID, rows[i].Email); !errors.Is(err, repository.ErrInvitationInvalid) {
				t.Fatalf("restored proof accepted or reconciled membership: %v", err)
			}
		}
		var memberships int64
		if err := target.Model(&domain.AccountUser{}).Count(&memberships).Error; err != nil || memberships != int64(len(members)) {
			t.Fatal("invitation checkpoint created membership")
		}
	}
	fresh := invite
	fresh.ID, fresh.Email, fresh.TokenDigest = 0, "fresh@example.test", invitationSnapshotDigest("fresh")
	if err := target.Create(&fresh).Error; err != nil || fresh.ID <= revoked.ID {
		t.Fatalf("invitation ID sequence not repaired: %d %v", fresh.ID, err)
	}
}

func TestInvitationIdentityOwnerSnapshotPreservesCheckpointWithoutGrants(t *testing.T) {
	testInvitationIdentityOwnerSnapshot(t, false)
}
func TestPostgresInvitationIdentityOwnerSnapshotPreservesCheckpointWithoutGrants(t *testing.T) {
	testInvitationIdentityOwnerSnapshot(t, true)
}
func testInvitationIdentityOwnerSnapshot(t *testing.T, postgres bool) {
	open := newModuleTestDB
	if postgres {
		open = postgresModuleDB
	}
	source, target := open(t, "IAM"), open(t, "IAM")
	_, identity := invitationSnapshotFixtures()
	user := domain.User{ID: identity.UserID, Email: identity.Email, Name: "Invitee", PasswordHash: "owner-password-hash", CreatedAt: identity.CreatedAt, UpdatedAt: identity.CreatedAt}
	if err := source.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&identity).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "iam.json")
	if err := CreateSnapshot(source, "IAM", "invitation-identity", path); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSnapshot(context.Background(), target, "IAM", "invitation-identity", path, nil); err != nil {
		t.Fatal(err)
	}
	var restored domain.InvitationIdentity
	if err := target.First(&restored, identity.ID).Error; err != nil {
		t.Fatal(err)
	}
	if string(invitationSnapshotRowsForComparison(t, []domain.InvitationIdentity{restored})) != string(invitationSnapshotRowsForComparison(t, []domain.InvitationIdentity{identity})) {
		t.Fatal("activation checkpoint changed")
	}
	var sessions, users int64
	if err := target.Model(&domain.UserSession{}).Count(&sessions).Error; err != nil || sessions != 0 {
		t.Fatal("checkpoint created session")
	}
	if err := target.Model(&domain.User{}).Count(&users).Error; err != nil || users != 1 {
		t.Fatal("checkpoint synthesized identity")
	}
	if target.Migrator().HasTable(&domain.AccountUser{}) || target.Migrator().HasTable(&domain.Account{}) {
		t.Fatal("IAM recovery created tenant authority")
	}
	freshUser := domain.User{Name: "Fresh", Email: "fresh@example.test", PasswordHash: "fresh-owner-hash"}
	if err := target.Create(&freshUser).Error; err != nil || freshUser.ID <= identity.UserID {
		t.Fatalf("identity sequence not repaired: %v", err)
	}
	fresh := identity
	fresh.ID, fresh.UserID, fresh.Email, fresh.OperationKey = 0, freshUser.ID, freshUser.Email, invitationSnapshotDigest("fresh-operation")
	if err := target.Create(&fresh).Error; err != nil || fresh.ID <= identity.ID {
		t.Fatalf("activation checkpoint sequence not repaired: %v", err)
	}
}

func rewriteInvitationSnapshot(t *testing.T, original []byte, path string, mutate func(*ModuleSnapshot)) {
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
	hash := sha256.Sum256(canonical)
	file.Checksum = hex.EncodeToString(hash[:])
	payload, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInvitationSnapshotCorruptionAndRebuildRollBackAtomically(t *testing.T) {
	testInvitationSnapshotRollback(t, false)
}
func TestPostgresInvitationSnapshotCorruptionAndRebuildRollBackAtomically(t *testing.T) {
	testInvitationSnapshotRollback(t, true)
}
func testInvitationSnapshotRollback(t *testing.T, postgres bool) {
	open := newModuleTestDB
	if postgres {
		open = postgresModuleDB
	}
	for _, module := range []string{"TEN", "IAM"} {
		t.Run(module, func(t *testing.T) {
			source, target := open(t, module), open(t, module)
			invite, identity := invitationSnapshotFixtures()
			var model any
			var sentinel any
			var load func(*testing.T) any
			mutations := map[string]any{}
			if module == "TEN" {
				invite.ID = 500
				model = &domain.AgentInvitation{}
				if err := source.Create(&invite).Error; err != nil {
					t.Fatal(err)
				}
				s := invite
				s.ID = 0
				s.Email = "sentinel@example.test"
				s.TokenDigest = invitationSnapshotDigest("sentinel")
				if err := target.Create(&s).Error; err != nil {
					t.Fatal(err)
				}
				sentinel = []domain.AgentInvitation{s}
				load = func(t *testing.T) any {
					var rows []domain.AgentInvitation
					if err := target.Order("id").Find(&rows).Error; err != nil {
						t.Fatal(err)
					}
					return rows
				}
				for name, edit := range map[string]func(*domain.AgentInvitation){
					"bad_digest":          func(r *domain.AgentInvitation) { r.TokenDigest = "plaintext" },
					"bad_role":            func(r *domain.AgentInvitation) { r.Role = "super_admin" },
					"bad_state":           func(r *domain.AgentInvitation) { r.DeliveryState = "sent" },
					"long_lifetime":       func(r *domain.AgentInvitation) { r.ExpiresAt = r.CreatedAt.Add(25 * time.Hour) },
					"bad_email":           func(r *domain.AgentInvitation) { r.Email = "Invitee@example.test" },
					"unbound_consumption": func(r *domain.AgentInvitation) { r.ConsumedAt = &r.CreatedAt },
					"zero_membership":     func(r *domain.AgentInvitation) { v := uint(0); r.AcceptedMembershipID = &v },
					"missing_fingerprint": func(r *domain.AgentInvitation) { v := uint(9); r.CustomRoleID = &v },
					"orphan_fingerprint":  func(r *domain.AgentInvitation) { r.RoleFingerprint = invitationSnapshotDigest("role") },
					"zero_inviter":        func(r *domain.AgentInvitation) { r.InviterID = 0 },
				} {
					bad := invite
					edit(&bad)
					mutations[name] = []domain.AgentInvitation{bad}
				}
			} else {
				identity.ID = 500
				model = &domain.InvitationIdentity{}
				u := domain.User{ID: identity.UserID, Name: "Source", Email: identity.Email, PasswordHash: "source-hash"}
				if err := source.Create(&u).Error; err != nil {
					t.Fatal(err)
				}
				if err := source.Create(&identity).Error; err != nil {
					t.Fatal(err)
				}
				u.ID = 0
				u.Email = "sentinel@example.test"
				if err := target.Create(&u).Error; err != nil {
					t.Fatal(err)
				}
				s := identity
				s.ID = 0
				s.UserID = u.ID
				s.Email = u.Email
				s.OperationKey = invitationSnapshotDigest("sentinel-operation")
				if err := target.Create(&s).Error; err != nil {
					t.Fatal(err)
				}
				sentinel = []domain.InvitationIdentity{s}
				load = func(t *testing.T) any {
					var rows []domain.InvitationIdentity
					if err := target.Order("id").Find(&rows).Error; err != nil {
						t.Fatal(err)
					}
					return rows
				}
				for name, edit := range map[string]func(*domain.InvitationIdentity){
					"bad_operation":    func(r *domain.InvitationIdentity) { r.OperationKey = "plaintext" },
					"zero_user":        func(r *domain.InvitationIdentity) { r.UserID = 0 },
					"missing_user":     func(r *domain.InvitationIdentity) { r.UserID = 999 },
					"mismatched_email": func(r *domain.InvitationIdentity) { r.Email = "other@example.test" },
					"zero_time":        func(r *domain.InvitationIdentity) { r.CreatedAt = time.Time{} },
				} {
					bad := identity
					edit(&bad)
					mutations[name] = []domain.InvitationIdentity{bad}
				}
			}
			marker := domain.LocalChangeJournal{ChangeID: "preexisting", SourceModule: module, AccountID: 91, ActorID: 2, ActorType: "system", Action: "create"}
			if err := target.Create(&marker).Error; err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "rollback.json")
			if err := CreateSnapshot(source, module, "invitation-rollback", path); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			table, err := modelTable(source, model)
			if err != nil {
				t.Fatal(err)
			}
			verify := func(t *testing.T) {
				t.Helper()
				before := invitationSnapshotRowsForComparison(t, sentinel)
				after := invitationSnapshotRowsForComparison(t, load(t))
				if string(before) != string(after) {
					t.Fatal("failed restore changed target checkpoint")
				}
				var markers int64
				if err := target.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", marker.ChangeID).Count(&markers).Error; err != nil || markers != 1 {
					t.Fatal("failed restore erased target journal")
				}
				if err := target.Model(&domain.LocalChangeJournal{}).Count(&markers).Error; err != nil || markers != 1 {
					t.Fatal("failed restore wrote completion marker")
				}
			}
			for name, bad := range mutations {
				t.Run(name, func(t *testing.T) {
					rewriteInvitationSnapshot(t, original, path, func(s *ModuleSnapshot) { s.Tables[table], _ = marshalSnapshotRows(bad) })
					if err := RestoreSnapshot(context.Background(), target, module, "invitation-rollback", path, nil); err == nil {
						t.Fatal("invalid checkpoint restore succeeded")
					}
					verify(t)
				})
			}
			for name, raw := range map[string]json.RawMessage{"public_only": json.RawMessage(`[{}]`), "missing_hidden_fields": json.RawMessage(`{"codec":"` + agentInvitationSnapshotCodec + `","rows":[{}]}`)} {
				t.Run(name, func(t *testing.T) {
					rewriteInvitationSnapshot(t, original, path, func(s *ModuleSnapshot) { s.Tables[table] = raw })
					if err := RestoreSnapshot(context.Background(), target, module, "invitation-rollback", path, nil); err == nil {
						t.Fatal("structurally corrupt checkpoint restore succeeded")
					}
					verify(t)
				})
			}
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := RestoreSnapshot(context.Background(), target, module, "invitation-rollback", path, func(context.Context, *gorm.DB) error { return errors.New("rebuild failed") }); err == nil {
				t.Fatal("failed rebuild committed")
			}
			verify(t)
			if module == "TEN" {
				fresh := invite
				fresh.ID = 0
				fresh.Email = "fresh@example.test"
				fresh.TokenDigest = invitationSnapshotDigest("new")
				if err := target.Create(&fresh).Error; err != nil || fresh.ID != 2 {
					t.Fatalf("failed restore changed sequence: %d %v", fresh.ID, err)
				}
			} else {
				fresh := identity
				fresh.ID = 0
				fresh.UserID = 99
				fresh.Email = "fresh@example.test"
				fresh.OperationKey = invitationSnapshotDigest("new")
				if err := target.Create(&fresh).Error; err != nil || fresh.ID != 2 {
					t.Fatalf("failed restore changed sequence: %d %v", fresh.ID, err)
				}
			}
		})
	}
}

func TestInvitationSnapshotRejectsUniqueKeyCollisions(t *testing.T) {
	invite, identity := invitationSnapshotFixtures()
	for _, collision := range []string{"id", "digest", "mailbox"} {
		first, second := invite, invite
		second.ID++
		second.Email = "second@example.test"
		second.TokenDigest = invitationSnapshotDigest("second-proof")
		switch collision {
		case "id":
			second.ID = first.ID
		case "digest":
			second.TokenDigest = first.TokenDigest
		case "mailbox":
			second.Email = first.Email
		}
		raw, err := marshalSnapshotRows([]domain.AgentInvitation{first, second})
		var target []domain.AgentInvitation
		if err != nil || unmarshalSnapshotRows(raw, &target) == nil || target != nil {
			t.Fatalf("invitation %s collision accepted or partially decoded", collision)
		}
	}
	// The same mailbox may legitimately have separate invitations in two tenants.
	second := invite
	second.ID++
	second.AccountID++
	second.TokenDigest = invitationSnapshotDigest("other-tenant")
	raw, err := marshalSnapshotRows([]domain.AgentInvitation{invite, second})
	var invites []domain.AgentInvitation
	if err != nil || unmarshalSnapshotRows(raw, &invites) != nil || len(invites) != 2 {
		t.Fatal("cross-tenant mailbox was incorrectly treated as a collision")
	}
	for _, collision := range []string{"id", "user", "operation"} {
		first, second := identity, identity
		second.ID++
		second.UserID++
		second.Email = "second@example.test"
		second.OperationKey = invitationSnapshotDigest("second-operation")
		switch collision {
		case "id":
			second.ID = first.ID
		case "user":
			second.UserID = first.UserID
		case "operation":
			second.OperationKey = first.OperationKey
		}
		raw, err := marshalSnapshotRows([]domain.InvitationIdentity{first, second})
		var target []domain.InvitationIdentity
		if err != nil || unmarshalSnapshotRows(raw, &target) == nil || target != nil {
			t.Fatalf("activation %s collision accepted or partially decoded", collision)
		}
	}
}

func TestInvitationEmptyOwnerSnapshots(t *testing.T) {
	testInvitationEmptyOwnerSnapshots(t, false)
}
func TestPostgresInvitationEmptyOwnerSnapshots(t *testing.T) {
	testInvitationEmptyOwnerSnapshots(t, true)
}
func testInvitationEmptyOwnerSnapshots(t *testing.T, postgres bool) {
	open := newModuleTestDB
	if postgres {
		open = postgresModuleDB
	}
	for _, module := range []string{"TEN", "IAM"} {
		t.Run(module, func(t *testing.T) {
			source, target := open(t, module), open(t, module)
			path := filepath.Join(t.TempDir(), "empty.json")
			if err := CreateSnapshot(source, module, "empty-invitation", path); err != nil {
				t.Fatal(err)
			}
			if err := RestoreSnapshot(context.Background(), target, module, "empty-invitation", path, nil); err != nil {
				t.Fatal(err)
			}
			var model any = &domain.AgentInvitation{}
			if module == "IAM" {
				model = &domain.InvitationIdentity{}
			}
			var count int64
			if err := target.Model(model).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("empty invitation snapshot fabricated a row")
			}
		})
	}
}
