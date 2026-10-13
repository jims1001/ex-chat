package modruntime

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/mail"
	"reflect"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// These codecs belong exclusively to private mode-0600 owner backups. They
// include every persisted field without exposing those fields in public JSON.
const (
	agentInvitationSnapshotCodec    = "ten.agent_invitation.v1"
	invitationIdentitySnapshotCodec = "iam.invitation_identity.v1"
)

type agentInvitationSnapshot struct {
	ID                   uint       `json:"id"`
	AccountID            uint       `json:"account_id"`
	Email                string     `json:"email"`
	Role                 string     `json:"role"`
	CustomRoleID         *uint      `json:"custom_role_id"`
	RoleFingerprint      string     `json:"role_fingerprint"`
	InviterID            uint       `json:"inviter_id"`
	InviterMembershipID  uint       `json:"inviter_membership_id"`
	IntendedUserID       *uint      `json:"intended_user_id"`
	TokenDigest          string     `json:"token_digest"`
	DeliveryState        string     `json:"delivery_state"`
	ExpiresAt            time.Time  `json:"expires_at"`
	ConsumedAt           *time.Time `json:"consumed_at"`
	AcceptedUserID       *uint      `json:"accepted_user_id"`
	AcceptedMembershipID *uint      `json:"accepted_membership_id"`
	RevokedAt            *time.Time `json:"revoked_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type invitationIdentitySnapshot struct {
	ID           uint      `json:"id"`
	OperationKey string    `json:"operation_key"`
	UserID       uint      `json:"user_id"`
	Email        string    `json:"email"`
	CreatedAt    time.Time `json:"created_at"`
}

type invitationSnapshotTable[T any] struct {
	Codec string `json:"codec"`
	Rows  []T    `json:"rows"`
}

func marshalAgentInvitationSnapshot(rows []domain.AgentInvitation) ([]byte, error) {
	table := invitationSnapshotTable[agentInvitationSnapshot]{Codec: agentInvitationSnapshotCodec, Rows: make([]agentInvitationSnapshot, len(rows))}
	for i, row := range rows {
		table.Rows[i] = agentInvitationSnapshot(row)
	}
	return json.Marshal(table)
}

func marshalInvitationIdentitySnapshot(rows []domain.InvitationIdentity) ([]byte, error) {
	table := invitationSnapshotTable[invitationIdentitySnapshot]{Codec: invitationIdentitySnapshotCodec, Rows: make([]invitationIdentitySnapshot, len(rows))}
	for i, row := range rows {
		table.Rows[i] = invitationIdentitySnapshot(row)
	}
	return json.Marshal(table)
}

// encoding/json permits duplicate keys and case aliases. A security checkpoint
// must have one unambiguous representation: reject duplicates recursively, then
// require the exact lowercase field inventory for both its table and each row.
func validateInvitationSnapshotJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var scan func() error
	scan = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("ambiguous invitation snapshot field")
				}
				seen[name] = true
				if err := scan(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := scan(); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid invitation snapshot structure")
		}
		_, err = decoder.Token()
		return err
	}
	if err := scan(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("invitation snapshot contains trailing data")
	}
	return nil
}

func decodeInvitationSnapshotTable[T any](raw []byte, codec string) ([]T, error) {
	if err := validateInvitationSnapshotJSON(raw); err != nil {
		return nil, err
	}
	var table invitationSnapshotTable[T]
	if err := decodeStrictSnapshotJSON(raw, &table); err != nil {
		return nil, err
	}
	if table.Codec != codec || table.Rows == nil {
		return nil, errors.New("unsupported invitation snapshot codec")
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil || len(shape) != 2 || shape["codec"] == nil || shape["rows"] == nil {
		return nil, errors.New("invalid invitation snapshot table fields")
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(shape["rows"], &rows); err != nil {
		return nil, err
	}
	var item T
	typ := reflect.TypeOf(item)
	for _, row := range rows {
		if len(row) != typ.NumField() {
			return nil, errors.New("incomplete invitation snapshot row")
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			value, present := row[field.Tag.Get("json")]
			if !present || (field.Type.Kind() != reflect.Pointer && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
				return nil, errors.New("missing or null invitation snapshot field")
			}
		}
	}
	return table.Rows, nil
}

func validInvitationSnapshotDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validInvitationSnapshotEmail(value string) bool {
	if value == "" || len(value) > 255 || strings.ToLower(strings.TrimSpace(value)) != value {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}

func validAgentInvitationSnapshot(row domain.AgentInvitation) bool {
	if row.ID == 0 || row.AccountID == 0 || row.InviterID == 0 || row.InviterMembershipID == 0 || !validInvitationSnapshotEmail(row.Email) ||
		!validInvitationSnapshotDigest(row.TokenDigest) || row.CreatedAt.IsZero() || row.UpdatedAt.Before(row.CreatedAt) ||
		!row.ExpiresAt.After(row.CreatedAt) || row.ExpiresAt.Sub(row.CreatedAt) > 24*time.Hour+time.Minute {
		return false
	}
	if row.Role != domain.RoleAgent && row.Role != domain.RoleAdministrator {
		return false
	}
	if row.CustomRoleID == nil {
		if row.RoleFingerprint != "" {
			return false
		}
	} else if *row.CustomRoleID == 0 || row.Role != domain.RoleAgent || !validInvitationSnapshotDigest(row.RoleFingerprint) {
		return false
	}
	if row.IntendedUserID != nil && *row.IntendedUserID == 0 {
		return false
	}
	if (row.ConsumedAt == nil) != (row.AcceptedUserID == nil) || (row.ConsumedAt == nil) != (row.AcceptedMembershipID == nil) {
		return false
	}
	if row.AcceptedMembershipID != nil && *row.AcceptedMembershipID == 0 {
		return false
	}
	if row.AcceptedUserID != nil && (*row.AcceptedUserID == 0 || (row.IntendedUserID != nil && *row.AcceptedUserID != *row.IntendedUserID)) {
		return false
	}
	for _, stamp := range []*time.Time{row.ConsumedAt, row.RevokedAt} {
		if stamp != nil && (stamp.IsZero() || stamp.Before(row.CreatedAt)) {
			return false
		}
	}
	switch row.DeliveryState {
	case "pending_delivery", "submitted", "captured_mock", "delivery_unknown":
		return true
	default:
		return false
	}
}

func validInvitationIdentitySnapshot(row domain.InvitationIdentity) bool {
	return row.ID != 0 && row.UserID != 0 && validInvitationSnapshotDigest(row.OperationKey) && validInvitationSnapshotEmail(row.Email) && !row.CreatedAt.IsZero()
}

func unmarshalAgentInvitationSnapshot(raw []byte, target *[]domain.AgentInvitation) error {
	rows, err := decodeInvitationSnapshotTable[agentInvitationSnapshot](raw, agentInvitationSnapshotCodec)
	if err != nil {
		return err
	}
	result := make([]domain.AgentInvitation, len(rows))
	ids, digests := map[uint]bool{}, map[string]bool{}
	type mailbox struct {
		accountID uint
		email     string
	}
	mailboxes := map[mailbox]bool{}
	for i, row := range rows {
		value := domain.AgentInvitation(row)
		key := mailbox{row.AccountID, row.Email}
		if !validAgentInvitationSnapshot(value) || ids[row.ID] || digests[row.TokenDigest] || mailboxes[key] {
			return errors.New("invalid agent invitation checkpoint")
		}
		ids[row.ID], digests[row.TokenDigest], mailboxes[key] = true, true, true
		result[i] = value
	}
	*target = result
	return nil
}

func unmarshalInvitationIdentitySnapshot(raw []byte, target *[]domain.InvitationIdentity) error {
	rows, err := decodeInvitationSnapshotTable[invitationIdentitySnapshot](raw, invitationIdentitySnapshotCodec)
	if err != nil {
		return err
	}
	result := make([]domain.InvitationIdentity, len(rows))
	ids, users, operations := map[uint]bool{}, map[uint]bool{}, map[string]bool{}
	for i, row := range rows {
		value := domain.InvitationIdentity(row)
		if !validInvitationIdentitySnapshot(value) || ids[row.ID] || users[row.UserID] || operations[row.OperationKey] {
			return errors.New("invalid invitation identity checkpoint")
		}
		ids[row.ID], users[row.UserID], operations[row.OperationKey] = true, true, true
		result[i] = value
	}
	*target = result
	return nil
}

// A backup is not an email invitation continuation. Every pending row becomes
// a deterministic terminal tombstone, even when the snapshot is restored before
// expiry. Consumed proofs are also revoked so recovery cannot resume acceptance
// reconciliation. Preserve consumed/revoked timestamps and identity facts.
func restoreInvitationSnapshotRows(tx *gorm.DB, rows any) (bool, error) {
	invitations, ok := rows.(*[]domain.AgentInvitation)
	if !ok {
		return false, nil
	}
	for i := range *invitations {
		row := &(*invitations)[i]
		if row.RevokedAt == nil {
			stamp := row.ExpiresAt
			row.RevokedAt = &stamp
		}
	}
	if len(*invitations) == 0 {
		return true, nil
	}
	return true, tx.Create(invitations).Error
}

// Checkpoints only witness a matching IAM identity that already exists in this
// owner snapshot. They never synthesize users, logins, sessions or membership.
// This is checked after all IAM tables are restored, inside the transaction.
func validateRestoredInvitationIdentities(tx *gorm.DB) error {
	var identities []domain.InvitationIdentity
	if err := tx.Find(&identities).Error; err != nil {
		return err
	}
	for _, identity := range identities {
		var count int64
		if err := tx.Model(&domain.User{}).Where("id = ? AND email = ?", identity.UserID, identity.Email).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errors.New("invitation checkpoint does not match restored IAM identity")
		}
	}
	return nil
}
