package handler

import (
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type TeamHandler struct {
	teamRepo *repository.TeamRepository
}

func NewTeamHandler(teamRepo *repository.TeamRepository) *TeamHandler {
	return &TeamHandler{teamRepo: teamRepo}
}

type CreateTeamRequest struct {
	Name            string `json:"name" binding:"required"`
	Description     string `json:"description"`
	AllowAutoAssign *bool  `json:"allow_auto_assign"`
}

type AddTeamMembersRequest struct {
	UserIDs []uint `json:"user_ids" binding:"required"`
}

func (h *TeamHandler) ListTeams(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	teams, err := h.teamRepo.List(accountID)
	if err != nil {
		response.InternalError(c, "Failed to list teams")
		return
	}

	response.Success(c, teams)
}

func (h *TeamHandler) CreateTeam(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	allowAutoAssign := true
	if req.AllowAutoAssign != nil {
		allowAutoAssign = *req.AllowAutoAssign
	}

	team := domain.Team{
		AccountID:       accountID,
		Name:            req.Name,
		Description:     req.Description,
		AllowAutoAssign: allowAutoAssign,
	}

	if err := h.teamRepo.Create(&team); err != nil {
		logger.WithComponent("team").Error("failed to create team",
			"account_id", accountID,
			"name", req.Name,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create team")
		return
	}

	logger.WithComponent("team").Info("team created",
		"account_id", accountID,
		"team_id", team.ID,
		"name", team.Name,
	)

	response.Created(c, team)
}

func (h *TeamHandler) GetTeam(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid team ID")
		return
	}

	team, err := h.teamRepo.FindByID(accountID, uint(id))
	if err != nil || team == nil {
		response.NotFound(c, "Team not found")
		return
	}

	response.Success(c, team)
}

func (h *TeamHandler) UpdateTeam(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid team ID")
		return
	}

	team, err := h.teamRepo.FindByID(accountID, uint(id))
	if err != nil || team == nil {
		response.NotFound(c, "Team not found")
		return
	}

	var req CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.Name != "" {
		team.Name = req.Name
	}
	if req.Description != "" {
		team.Description = req.Description
	}
	if req.AllowAutoAssign != nil {
		team.AllowAutoAssign = *req.AllowAutoAssign
	}

	if err := h.teamRepo.Update(team); err != nil {
		logger.WithComponent("team").Error("failed to update team",
			"account_id", accountID,
			"team_id", team.ID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update team")
		return
	}

	logger.WithComponent("team").Info("team updated",
		"account_id", accountID,
		"team_id", team.ID,
		"name", team.Name,
	)

	response.Success(c, team)
}

func (h *TeamHandler) DeleteTeam(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid team ID")
		return
	}

	if err := h.teamRepo.Delete(accountID, uint(id)); err != nil {
		logger.WithComponent("team").Error("failed to delete team",
			"account_id", accountID,
			"team_id", id,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete team")
		return
	}

	logger.WithComponent("team").Info("team deleted",
		"account_id", accountID,
		"team_id", id,
	)

	response.Success(c, gin.H{"deleted": true})
}

func (h *TeamHandler) AddMembers(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid team ID")
		return
	}

	var req AddTeamMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	for _, uid := range req.UserIDs {
		_ = h.teamRepo.AddMember(uint(id), uid)
	}

	logger.WithComponent("team").Info("team members added",
		"team_id", id,
		"user_ids", req.UserIDs,
	)

	members, _ := h.teamRepo.ListMembers(uint(id))
	response.Success(c, members)
}

func (h *TeamHandler) ListMembers(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid team ID")
		return
	}

	members, err := h.teamRepo.ListMembers(uint(id))
	if err != nil {
		response.InternalError(c, "Failed to list members")
		return
	}

	response.Success(c, members)
}

func (h *TeamHandler) RemoveMember(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid team ID")
		return
	}
	memberID, err := strconv.ParseUint(c.Param("member_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid member ID")
		return
	}

	_ = h.teamRepo.RemoveMember(uint(id), uint(memberID))

	logger.WithComponent("team").Info("team member removed",
		"team_id", id,
		"member_id", memberID,
	)

	response.Success(c, gin.H{"deleted": true})
}
