package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AgentInvitationHandler is API-owned orchestration over separate IAM, TEN and
// mail ports. It does not give TEN a dependency on MSG or access to IAM storage.
type AgentInvitationHandler struct {
	accountRepo  *repository.AccountRepository
	userRepo     *repository.UserRepository
	emailService *service.EmailService
}

func NewAgentInvitationHandler(accounts *repository.AccountRepository, users *repository.UserRepository, emails *service.EmailService) *AgentInvitationHandler {
	return &AgentInvitationHandler{accountRepo: accounts, userRepo: users, emailService: emails}
}

func (h *AgentInvitationHandler) SetEmailService(emailService *service.EmailService) {
	h.emailService = emailService
}
func respondAgentInvitationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrInvitationForbidden):
		response.Forbidden(c, "Invitation or role management permission denied")
	case errors.Is(err, repository.ErrInvitationInvalid):
		response.BadRequest(c, "Invitation is invalid, expired or no longer authorized")
	case errors.Is(err, repository.ErrInvitationSignInRequired):
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "sign_in_required", "message": "Sign in to the intended account to accept this invitation"})
	case errors.Is(err, repository.ErrInvitationMemberExists):
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "membership_exists", "message": "User is already a member"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		response.NotFound(c, "Membership not found")
	default:
		response.InternalError(c, "Unable to complete invitation operation")
	}
}
func (h *AgentInvitationHandler) AddAgent(c *gin.Context) {
	accountID := c.MustGet(middleware.ContextAccountID).(uint)
	actorID := c.MustGet(middleware.ContextUserID).(uint)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var req AddAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload")
		return
	}
	if req.Password != nil {
		response.BadRequest(c, "Agent passwords cannot be supplied; use the mailed invitation")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Role == "" {
		req.Role = domain.RoleAgent
	}
	if h.emailService == nil || h.emailService.CheckAgentInvitationConfiguration() != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Invitation email is not configured"})
		return
	}
	intended, err := h.userRepo.FindInvitationIdentityByEmail(req.Email)
	if err != nil {
		respondAgentInvitationError(c, err)
		return
	}
	var intendedID *uint
	if intended != nil {
		intendedID = &intended.ID
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		response.InternalError(c, "Unable to create invitation")
		return
	}
	token := hex.EncodeToString(raw)
	digest, _ := repository.AgentInvitationTokenDigest(token)
	invitation, err := h.accountRepo.CreateAgentInvitation(accountID, actorID, req.Email, req.Role, req.CustomRoleID, digest, intendedID)
	if err != nil {
		respondAgentInvitationError(c, err)
		return
	}
	state, sendErr := h.emailService.SendAgentInvitationEmail(c.Request.Context(), req.Email, token, accountID, invitation.ExpiresAt)
	if sendErr != nil {
		// SMTP failure can mean the provider accepted the message. Never claim
		// non-delivery, retry the same secret, or activate an uncertain proof.
		_ = h.accountRepo.SetAgentInvitationDelivery(invitation.ID, digest, "delivery_unknown")
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "data": gin.H{"id": invitation.ID, "delivery_state": "delivery_unknown"}, "message": "Invitation delivery is unconfirmed; reissue to send a new link"})
		return
	}
	if err := h.accountRepo.SetAgentInvitationDelivery(invitation.ID, digest, state); err != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Invitation status changed or could not be confirmed; reissue if needed"})
		return
	}
	invitation.DeliveryState = state
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": invitation})
}
func (h *AgentInvitationHandler) ListAgentInvitations(c *gin.Context) {
	invitations, err := h.accountRepo.ListAgentInvitations(c.MustGet(middleware.ContextAccountID).(uint), c.MustGet(middleware.ContextUserID).(uint))
	if err != nil {
		respondAgentInvitationError(c, err)
		return
	}
	response.Success(c, invitations)
}
func (h *AgentInvitationHandler) RevokeAgentInvitation(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid invitation ID")
		return
	}
	if err := h.accountRepo.RevokeAgentInvitation(c.MustGet(middleware.ContextAccountID).(uint), c.MustGet(middleware.ContextUserID).(uint), uint(id)); err != nil {
		respondAgentInvitationError(c, err)
		return
	}
	response.Success(c, gin.H{"revoked": true})
}
func (h *AgentInvitationHandler) AcceptAgentInvitation(c *gin.Context) {
	if os.Getenv("AGENT_INVITATIONS_ENABLED") != "true" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Agent invitations are disabled"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var req struct {
		Token   string `json:"token"`
		MFACode string `json:"mfa_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid invitation payload")
		return
	}
	user, ok := c.MustGet(middleware.ContextUser).(*domain.User)
	if !ok || user == nil {
		response.Unauthorized(c, "Sign in to accept the invitation")
		return
	}
	invitation, err := h.accountRepo.InspectAgentInvitation(req.Token)
	if err != nil {
		respondAgentInvitationError(c, err)
		return
	}
	if invitation.Email != strings.ToLower(strings.TrimSpace(user.Email)) || (invitation.IntendedUserID != nil && *invitation.IntendedUserID != user.ID) {
		respondAgentInvitationError(c, repository.ErrInvitationInvalid)
		return
	}
	if err := h.userRepo.AuthorizeInvitationAcceptance(user.ID, user.AuthVersion, user.Email, invitation.TokenDigest, req.MFACode); err != nil {
		if errors.Is(err, repository.ErrMFAInvalidCode) || errors.Is(err, repository.ErrMFAReplay) || errors.Is(err, repository.ErrMFARateLimited) {
			respondMFAVerificationError(c, err)
		} else {
			respondAgentInvitationError(c, err)
		}
		return
	}
	member, err := h.accountRepo.AcceptAgentInvitation(req.Token, user.ID, invitation.Email)
	if err != nil {
		respondAgentInvitationError(c, err)
		return
	}
	response.Success(c, member)
}
func (h *AgentInvitationHandler) ActivateAgentInvitation(c *gin.Context) {
	if os.Getenv("AGENT_INVITATIONS_ENABLED") != "true" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Agent invitations are disabled"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var req struct {
		Token                string `json:"token"`
		Name                 string `json:"name"`
		Password             string `json:"password"`
		PasswordConfirmation string `json:"password_confirmation"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid activation payload")
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 255 || len(req.Password) < 8 || len(req.Password) > 72 || req.Password != req.PasswordConfirmation {
		response.BadRequest(c, "Name and matching passwords of 8 to 72 bytes are required")
		return
	}
	invitation, err := h.accountRepo.InspectAgentInvitation(req.Token)
	if err != nil {
		respondAgentInvitationError(c, err)
		return
	}
	if invitation.IntendedUserID != nil {
		respondAgentInvitationError(c, repository.ErrInvitationSignInRequired)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		response.InternalError(c, "Unable to process activation")
		return
	}
	user, err := h.userRepo.ActivateInvitedIdentity(invitation, req.Name, hash)
	if err != nil {
		respondAgentInvitationError(c, err)
		return
	}
	// An operation-bound retry returns the immutable first-created identity.
	// It must not suggest that a different retry password was installed.
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		respondAgentInvitationError(c, repository.ErrInvitationSignInRequired)
		return
	}
	if err := h.userRepo.AuthorizeInvitationAcceptance(user.ID, user.AuthVersion, user.Email, invitation.TokenDigest, ""); err != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "activation_incomplete", "message": "Your identity exists; sign in to complete invitation acceptance"})
		return
	}
	if _, err := h.accountRepo.AcceptAgentInvitation(req.Token, user.ID, invitation.Email); err != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "activation_incomplete", "message": "Your identity exists, but tenant access was not confirmed. Sign in and retry the invitation or request a new link"})
		return
	}
	response.Success(c, gin.H{"activated": true, "account_id": invitation.AccountID, "message": "Invitation accepted; sign in with your password"})
}

// PreviewAgentInvitation supplies server-bound grant details for recipient
// consent. Fragment display values are not authoritative. It grants no access.
func (h *AgentInvitationHandler) PreviewAgentInvitation(c *gin.Context) {
	if os.Getenv("AGENT_INVITATIONS_ENABLED") != "true" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Agent invitations are disabled"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var req struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid invitation payload")
		return
	}
	invite, err := h.accountRepo.InspectAgentInvitation(req.Token)
	if err != nil {
		respondAgentInvitationError(c, err)
		return
	}
	account, err := h.accountRepo.FindByID(invite.AccountID)
	if err != nil || account == nil {
		respondAgentInvitationError(c, repository.ErrInvitationInvalid)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"account_id": account.ID, "account_name": account.Name, "role": invite.Role, "custom_role_id": invite.CustomRoleID, "expires_at": invite.ExpiresAt, "accepted": invite.ConsumedAt != nil})
}
