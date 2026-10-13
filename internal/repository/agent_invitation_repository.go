package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvitationInvalid      = errors.New("invitation is invalid, expired or no longer authorized")
	ErrInvitationForbidden    = errors.New("invitation permission denied")
	ErrInvitationMemberExists = errors.New("user is already a member")
)

const AgentInvitationLifetime = 24 * time.Hour

func validInvitationDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func AgentInvitationTokenDigest(raw string) (string, error) {
	if !validInvitationDigest(raw) {
		return "", ErrInvitationInvalid
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:]), nil
}
func roleFingerprint(role *domain.CustomRole) string {
	raw, _ := json.Marshal([]string{role.Name, role.Permissions})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func lockAccountMembershipWrites(tx *gorm.DB, accountID uint) error {
	locked := tx.Model(&domain.Account{}).Where("id = ?", accountID).UpdateColumn("id", gorm.Expr("id"))
	if locked.Error != nil {
		return locked.Error
	}
	if locked.RowsAffected != 1 {
		return ErrInvitationInvalid
	}
	return nil
}
func lockInvitationAccount(tx *gorm.DB, accountID uint) error {
	if err := lockAccountMembershipWrites(tx, accountID); err != nil {
		return err
	}
	var account domain.Account
	if err := tx.First(&account, accountID).Error; err != nil {
		return err
	}
	if account.Status != "" && account.Status != "active" {
		return ErrInvitationInvalid
	}
	return nil
}

// Take the first write lock before reading an invitation. SQLite cannot safely
// upgrade competing read transactions; PostgreSQL locks the account row shared
// by all invite changes for the tenant.
func lockInvitationAccountForDigest(tx *gorm.DB, digest string) error {
	candidate := tx.Model(&domain.AgentInvitation{}).Select("account_id").Where("token_digest = ?", digest)
	result := tx.Model(&domain.Account{}).Where("id IN (?)", candidate).UpdateColumn("id", gorm.Expr("id"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvitationInvalid
	}
	return nil
}
func invitationManager(tx *gorm.DB, accountID, userID uint) (*domain.AccountUser, error) {
	var member domain.AccountUser
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND user_id = ?", accountID, userID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvitationForbidden
		}
		return nil, err
	}
	if member.Role == domain.RoleAdministrator {
		return &member, nil
	}
	var custom domain.CustomRole
	if member.CustomRoleID != nil {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND account_id = ?", *member.CustomRoleID, accountID).First(&custom).Error; err != nil {
			return nil, ErrInvitationForbidden
		}
	} else if member.Role != domain.RoleAgent && member.Role != "" {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("name = ? AND account_id = ?", member.Role, accountID).First(&custom).Error; err != nil {
			return nil, ErrInvitationForbidden
		}
	}
	if custom.ID == 0 || !domain.CheckPermissionInJSON(custom.Permissions, domain.PermissionAgentManage) {
		return nil, ErrInvitationForbidden
	}
	return &member, nil
}
func validateInvitationGrant(tx *gorm.DB, accountID uint, manager *domain.AccountUser, role string, customRoleID *uint) (string, error) {
	if role != domain.RoleAgent && role != domain.RoleAdministrator {
		return "", ErrInvitationInvalid
	}
	if customRoleID != nil && (role != domain.RoleAgent || *customRoleID == 0) {
		return "", ErrInvitationInvalid
	}
	if manager.Role != domain.RoleAdministrator && (role != domain.RoleAgent || customRoleID != nil) {
		return "", ErrInvitationForbidden
	}
	if customRoleID == nil {
		return "", nil
	}
	var custom domain.CustomRole
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ?", accountID, *customRoleID).First(&custom).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrInvitationInvalid
		}
		return "", err
	}
	return roleFingerprint(&custom), nil
}
func validatePendingInvitation(tx *gorm.DB, invite *domain.AgentInvitation) error {
	if invite.RevokedAt != nil || !invite.ExpiresAt.After(time.Now()) || (invite.DeliveryState != "submitted" && invite.DeliveryState != "captured_mock") {
		return ErrInvitationInvalid
	}
	if invite.ConsumedAt != nil {
		return nil
	} // Read-only reconciliation checked against exact membership at commit.
	manager, err := invitationManager(tx, invite.AccountID, invite.InviterID)
	if err != nil {
		return err
	}
	if manager.ID != invite.InviterMembershipID {
		return ErrInvitationInvalid
	}
	fingerprint, err := validateInvitationGrant(tx, invite.AccountID, manager, invite.Role, invite.CustomRoleID)
	if err != nil {
		return err
	}
	if fingerprint != invite.RoleFingerprint {
		return ErrInvitationInvalid
	}
	return nil
}

// CreateAgentInvitation replaces the prior proof for this tenant/mailbox. TEN
// stores only its digest; IAM identity lookups happen through its separate port.
func (r *AccountRepository) CreateAgentInvitation(accountID, inviterID uint, email, role string, customRoleID *uint, digest string, intendedUserID *uint) (*domain.AgentInvitation, error) {
	if !validInvitationDigest(digest) || email == "" || email != strings.ToLower(strings.TrimSpace(email)) || len(email) > 255 {
		return nil, ErrInvitationInvalid
	}
	var invite domain.AgentInvitation
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockInvitationAccount(tx, accountID); err != nil {
			return err
		}
		manager, err := invitationManager(tx, accountID, inviterID)
		if err != nil {
			return err
		}
		fingerprint, err := validateInvitationGrant(tx, accountID, manager, role, customRoleID)
		if err != nil {
			return err
		}
		if intendedUserID != nil {
			var count int64
			if err := tx.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", accountID, *intendedUserID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return ErrInvitationMemberExists
			}
		}
		err = tx.Where("account_id = ? AND email = ?", accountID, email).First(&invite).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now().UTC()
		id := invite.ID
		invite = domain.AgentInvitation{ID: id, AccountID: accountID, Email: email, Role: role, CustomRoleID: customRoleID, RoleFingerprint: fingerprint, InviterID: inviterID, InviterMembershipID: manager.ID, IntendedUserID: intendedUserID, TokenDigest: digest, DeliveryState: "pending_delivery", ExpiresAt: now.Add(AgentInvitationLifetime), CreatedAt: now, UpdatedAt: now}
		if id == 0 {
			return tx.Create(&invite).Error
		}
		return tx.Model(&domain.AgentInvitation{}).Where("id = ?", id).Select("*").Omit("id").Updates(&invite).Error
	})
	if err != nil {
		return nil, err
	}
	return &invite, nil
}
func (r *AccountRepository) SetAgentInvitationDelivery(id uint, digest, state string) error {
	if state != "submitted" && state != "captured_mock" && state != "delivery_unknown" {
		return ErrInvitationInvalid
	}
	result := r.db.Model(&domain.AgentInvitation{}).Where("id = ? AND token_digest = ? AND delivery_state = ? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > ?", id, digest, "pending_delivery", time.Now()).Update("delivery_state", state)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvitationInvalid
	}
	return nil
}
func (r *AccountRepository) InspectAgentInvitation(rawToken string) (*domain.AgentInvitation, error) {
	digest, err := AgentInvitationTokenDigest(rawToken)
	if err != nil {
		return nil, err
	}
	var invite domain.AgentInvitation
	err = r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockInvitationAccountForDigest(tx, digest); err != nil {
			return err
		}
		if err := tx.Where("token_digest = ?", digest).First(&invite).Error; err != nil {
			return ErrInvitationInvalid
		}
		if err := lockInvitationAccount(tx, invite.AccountID); err != nil {
			return err
		}
		return validatePendingInvitation(tx, &invite)
	})
	if err != nil {
		return nil, err
	}
	return &invite, nil
}

// AcceptAgentInvitation commits only TEN data. The caller supplies an IAM-
// attested identity. Account serialization prevents concurrent invite accepts
// from duplicating a membership; consumed proofs can only reconcile their exact
// surviving membership, never recreate one after removal.
func (r *AccountRepository) AcceptAgentInvitation(rawToken string, userID uint, email string) (*domain.AccountUser, error) {
	digest, err := AgentInvitationTokenDigest(rawToken)
	if err != nil {
		return nil, err
	}
	var member domain.AccountUser
	err = r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockInvitationAccountForDigest(tx, digest); err != nil {
			return err
		}
		var invite domain.AgentInvitation
		if err := tx.Where("token_digest = ?", digest).First(&invite).Error; err != nil {
			return ErrInvitationInvalid
		}
		if err := lockInvitationAccount(tx, invite.AccountID); err != nil {
			return err
		}
		if err := validatePendingInvitation(tx, &invite); err != nil {
			return err
		}
		if userID == 0 || invite.Email != email || (invite.IntendedUserID != nil && *invite.IntendedUserID != userID) {
			return ErrInvitationInvalid
		}
		if invite.ConsumedAt != nil {
			if invite.AcceptedUserID == nil || *invite.AcceptedUserID != userID || invite.AcceptedMembershipID == nil {
				return ErrInvitationInvalid
			}
			if err := tx.Where("id = ? AND account_id = ? AND user_id = ?", *invite.AcceptedMembershipID, invite.AccountID, userID).First(&member).Error; err != nil {
				return ErrInvitationInvalid
			}
			return nil
		}
		var count int64
		if err := tx.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", invite.AccountID, userID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrInvitationMemberExists
		}
		member = domain.AccountUser{AccountID: invite.AccountID, UserID: userID, Role: invite.Role, CustomRoleID: invite.CustomRoleID, Availability: domain.AvailabilityOnline}
		if err := tx.Create(&member).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		result := tx.Model(&domain.AgentInvitation{}).Where("id = ? AND token_digest = ? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > ?", invite.ID, digest, now).Updates(map[string]any{"consumed_at": now, "accepted_user_id": userID, "accepted_membership_id": member.ID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvitationInvalid
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &member, nil
}
func (r *AccountRepository) ListAgentInvitations(accountID, actorID uint) ([]domain.AgentInvitation, error) {
	var invitations []domain.AgentInvitation
	err := r.db.Transaction(func(tx *gorm.DB) error {
		manager, err := invitationManager(tx, accountID, actorID)
		if err != nil {
			return err
		}
		query := tx.Where("account_id = ?", accountID)
		if manager.Role != domain.RoleAdministrator {
			query = query.Where("inviter_id = ?", actorID)
		}
		return query.Order("id DESC").Limit(100).Find(&invitations).Error
	})
	return invitations, err
}
func (r *AccountRepository) RevokeAgentInvitation(accountID, actorID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockInvitationAccount(tx, accountID); err != nil {
			return err
		}
		manager, err := invitationManager(tx, accountID, actorID)
		if err != nil {
			return err
		}
		query := tx.Model(&domain.AgentInvitation{}).Where("id = ? AND account_id = ?", id, accountID)
		if manager.Role != domain.RoleAdministrator {
			query = query.Where("inviter_id = ?", actorID)
		}
		result := query.Update("revoked_at", time.Now().UTC())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvitationInvalid
		}
		return nil
	})
}

// UpdateAgentMembership changes TEN fields only. Granting roles requires a
// tenant administrator; custom AgentManage grants only ordinary agent invites.
func (r *AccountRepository) UpdateAgentMembership(accountID, actorID, userID uint, role string, customRoleID *uint, availability string) (*domain.AccountUser, error) {
	var member domain.AccountUser
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockInvitationAccount(tx, accountID); err != nil {
			return err
		}
		manager, err := invitationManager(tx, accountID, actorID)
		if err != nil {
			return err
		}
		if err := tx.Where("account_id = ? AND user_id = ?", accountID, userID).First(&member).Error; err != nil {
			return err
		}
		originalRole, originalCustomRole := member.Role, member.CustomRoleID
		grants := role != "" || customRoleID != nil
		if grants && manager.Role != domain.RoleAdministrator {
			return ErrInvitationForbidden
		}
		if role != "" {
			member.Role = role
		}
		if customRoleID != nil {
			if *customRoleID == 0 {
				member.CustomRoleID = nil
			} else {
				member.CustomRoleID = customRoleID
			}
		}
		if grants {
			if _, err := validateInvitationGrant(tx, accountID, manager, member.Role, member.CustomRoleID); err != nil {
				return err
			}
		}
		if availability != "" {
			if availability != domain.AvailabilityOnline && availability != "offline" && availability != "busy" {
				return ErrInvitationInvalid
			}
			member.Availability = availability
		}
		if err := tx.Model(&domain.AccountUser{}).Where("id = ?", member.ID).Updates(map[string]any{"role": member.Role, "custom_role_id": member.CustomRoleID, "availability": member.Availability}).Error; err != nil {
			return err
		}
		customChanged := (originalCustomRole == nil) != (member.CustomRoleID == nil)
		if originalCustomRole != nil && member.CustomRoleID != nil {
			customChanged = *originalCustomRole != *member.CustomRoleID
		}
		if originalRole != member.Role || customChanged {
			return tx.Model(&domain.AgentInvitation{}).Where("account_id = ? AND inviter_id = ? AND consumed_at IS NULL AND revoked_at IS NULL", accountID, userID).Update("revoked_at", time.Now().UTC()).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &member, nil
}

// RemoveAgentMembership revokes the removed person's invitation proofs and
// invitations they issued, so removal cannot be undone using an old mailbox link.
func (r *AccountRepository) RemoveAgentMembership(accountID, actorID, userID uint, email string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockInvitationAccount(tx, accountID); err != nil {
			return err
		}
		manager, err := invitationManager(tx, accountID, actorID)
		if err != nil {
			return err
		}
		var member domain.AccountUser
		if err := tx.Where("account_id = ? AND user_id = ?", accountID, userID).First(&member).Error; err != nil {
			return err
		}
		if manager.Role != domain.RoleAdministrator && (member.Role != domain.RoleAgent || member.CustomRoleID != nil) {
			return ErrInvitationForbidden
		}
		if err := tx.Where("id = ?", member.ID).Delete(&domain.AccountUser{}).Error; err != nil {
			return err
		}
		return tx.Model(&domain.AgentInvitation{}).Where("account_id = ? AND (inviter_id = ? OR intended_user_id = ? OR accepted_user_id = ? OR email = ?)", accountID, userID, userID, userID, strings.ToLower(email)).Update("revoked_at", time.Now().UTC()).Error
	})
}
