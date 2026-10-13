package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// InstanceStatusHandler reports installation diagnostics after the router's
// existing authentication and live SuperAdmin authorization middleware.
type InstanceStatusHandler struct{ database string }

func NewInstanceStatusHandler(db *gorm.DB) *InstanceStatusHandler {
	return &InstanceStatusHandler{database: db.Dialector.Name()}
}

func (h *InstanceStatusHandler) Get(c *gin.Context) {
	// These two strings remain compatibility placeholders, not actual readiness
	// or a running-release assertion. Their replacement requires a product contract.
	c.JSON(http.StatusOK, gin.H{"status": "online", "version": "4.16.0", "database": h.database})
}
