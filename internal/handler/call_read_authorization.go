package handler

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// CallReadAuthorization composes current owner facts for the metadata GETs only.
// It does not establish a recording-byte or call-mutation authorization policy.
type CallReadAuthorization struct {
	accounts      *repository.AccountRepository
	inboxes       *repository.InboxRepository
	contacts      *repository.ContactRepository
	conversations *repository.ConversationRepository
	teams         *repository.TeamRepository
}

type callReadActor struct {
	accountID   uint
	userID      uint
	privileged  bool
	plainAgent  bool
	permissions string
}

func (h *ChannelDriverHandler) SetCallReadAuthorization(accounts *repository.AccountRepository, teams *repository.TeamRepository) {
	h.callReads = &CallReadAuthorization{
		accounts: accounts, inboxes: h.inboxRepo, contacts: h.contactRepo,
		conversations: h.convRepo, teams: teams,
	}
}

func (h *ChannelDriverHandler) callReadActor(c *gin.Context, accountID uint) (*callReadActor, bool) {
	userID := c.GetUint(middleware.ContextUserID)
	rawUser, _ := c.Get(middleware.ContextUser)
	user, ok := rawUser.(*domain.User)
	if !ok || user == nil || user.ID == 0 || user.ID != userID {
		response.Unauthorized(c, "User context required")
		return nil, false
	}
	if h.callReads == nil || h.callReads.accounts == nil || h.repo == nil {
		response.InternalError(c, "Call access service unavailable")
		return nil, false
	}
	actor, err := h.callReads.actor(accountID, userID)
	if err != nil {
		response.InternalError(c, "Failed to verify call access")
		return nil, false
	}
	if actor == nil {
		response.Forbidden(c, "Account membership required")
		return nil, false
	}
	return actor, true
}

func (a *CallReadAuthorization) actor(accountID, userID uint) (*callReadActor, error) {
	member, err := a.accounts.GetCallReadMembership(accountID, userID)
	if err != nil || member == nil {
		return nil, err
	}
	actor := &callReadActor{accountID: accountID, userID: userID}
	actor.privileged = member.Role == domain.RoleAdministrator
	if member.CustomRole != nil {
		actor.permissions = member.CustomRole.Permissions
		// Same permission evaluator and target as AccountRepository.HasPermission;
		// use the single, tenant-validated membership snapshot above. This includes
		// report_manage, Chinese aliases and the administrator permission token.
		actor.privileged = actor.privileged || domain.CheckPermissionInJSON(actor.permissions, domain.PermissionReportView)
	}
	actor.plainAgent = member.Role == domain.RoleAgent && member.CustomRoleID == nil && member.CustomRole == nil
	return actor, nil
}

func (a *CallReadAuthorization) canRead(actor *callReadActor, call *domain.Call) (bool, error) {
	if actor == nil || call == nil || call.AccountID != actor.accountID {
		return false, nil
	}
	if actor.privileged {
		return true, nil
	}
	if call.AgentID == nil || *call.AgentID != actor.userID || call.ConversationID == nil || *call.ConversationID == 0 || call.InboxID == 0 || call.ContactID == 0 {
		return false, nil
	}
	if !actor.plainAgent && actor.permissions == "" {
		return false, nil
	}
	if a.inboxes == nil || a.contacts == nil || a.conversations == nil {
		return false, errors.New("call access dependency unavailable")
	}
	conversation, err := a.conversations.FindCallReadReference(actor.accountID, *call.ConversationID, call.InboxID, call.ContactID)
	if err != nil || conversation == nil {
		return false, err
	}
	inboxExists, err := a.inboxes.Exists(actor.accountID, call.InboxID)
	if err != nil || !inboxExists {
		return false, err
	}
	contactExists, err := a.contacts.Exists(actor.accountID, call.ContactID)
	if err != nil || !contactExists {
		return false, err
	}
	inboxMembers, err := a.inboxes.ListMemberIDs(actor.accountID, call.InboxID)
	if err != nil {
		return false, err
	}
	baseScope := callReadContains(inboxMembers, actor.userID)
	if !baseScope && conversation.TeamID != nil && *conversation.TeamID != 0 {
		if a.teams == nil {
			return false, errors.New("call access dependency unavailable")
		}
		teamMembers, err := a.teams.ListMemberIDs(actor.accountID, *conversation.TeamID)
		if err != nil {
			return false, err
		}
		baseScope = callReadContains(teamMembers, actor.userID)
	}
	if !baseScope {
		return false, nil
	}
	if actor.plainAgent || domain.CheckPermissionInJSON(actor.permissions, domain.PermissionConversationManage) {
		return true, nil
	}
	self := conversation.AssigneeID != nil && *conversation.AssigneeID == actor.userID
	if domain.CheckPermissionInJSON(actor.permissions, domain.PermissionConversationUnassignedManage) && (conversation.AssigneeID == nil || self) {
		return true, nil
	}
	if domain.CheckPermissionInJSON(actor.permissions, domain.PermissionConversationParticipatingManage) {
		if self {
			return true, nil
		}
		participants, err := a.conversations.ListParticipants(actor.accountID, conversation.ID)
		if err != nil {
			return false, err
		}
		for _, participant := range participants {
			if participant.UserID == actor.userID {
				return true, nil
			}
		}
	}
	return false, nil
}

func callReadContains(ids []uint, userID uint) bool {
	for _, id := range ids {
		if id == userID {
			return true
		}
	}
	return false
}

func (h *ChannelDriverHandler) allowCallRead(c *gin.Context, actor *callReadActor, call *domain.Call) bool {
	allowed, err := h.callReads.canRead(actor, call)
	if err != nil {
		response.InternalError(c, "Failed to verify call access")
		return false
	}
	if !allowed {
		response.NotFound(c, "Call not found")
		return false
	}
	return true
}
