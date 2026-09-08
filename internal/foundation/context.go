package foundation

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	HeaderRequestID      = "X-Request-Id"
	HeaderCorrelationID  = "X-Correlation-Id"
	HeaderIdempotencyKey = "Idempotency-Key"

	ContextKeyUnified = "unified_context"

	CurrentFoundationVersion = "4.16.0-foundation"
	CurrentContractVersion   = "v1"
)

// UnifiedContext 统一上下文（API-03 规范）
// 承载全链路追踪、租户、行为主体与因果关系
type UnifiedContext struct {
	FoundationVersion string    `json:"foundation_version"`
	ContractVersion   string    `json:"contract_version"`
	RequestID         string    `json:"request_id"`
	CorrelationID     string    `json:"correlation_id"`
	CausationID       string    `json:"causation_id,omitempty"`
	AccountID         uint      `json:"account_id,omitempty"`
	ActorType         string    `json:"actor_type"` // User, Contact, System, Automation
	ActorID           uint      `json:"actor_id,omitempty"`
	SourceType        string    `json:"source_type"` // API, Widget, Dashboard, Webhook
	SourceID          string    `json:"source_id,omitempty"`
	IdempotencyKey    string    `json:"idempotency_key,omitempty"`
	OccurredAt        time.Time `json:"occurred_at"`
}

func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// UnifiedContextMiddleware 拦截请求并初始化统一上下文
func UnifiedContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader(HeaderRequestID)
		if reqID == "" {
			reqID = GenerateUUID()
		}

		corrID := c.GetHeader(HeaderCorrelationID)
		if corrID == "" {
			corrID = reqID
		}

		idempotencyKey := c.GetHeader(HeaderIdempotencyKey)

		uCtx := &UnifiedContext{
			FoundationVersion: CurrentFoundationVersion,
			ContractVersion:   CurrentContractVersion,
			RequestID:         reqID,
			CorrelationID:     corrID,
			ActorType:         "Anonymous",
			SourceType:        "API",
			IdempotencyKey:    idempotencyKey,
			OccurredAt:        time.Now().UTC(),
		}

		c.Set(ContextKeyUnified, uCtx)

		// 响应头回显追踪标识
		c.Writer.Header().Set(HeaderRequestID, reqID)
		c.Writer.Header().Set(HeaderCorrelationID, corrID)

		c.Next()
	}
}

func GetUnifiedContext(c *gin.Context) *UnifiedContext {
	if raw, exists := c.Get(ContextKeyUnified); exists {
		if uCtx, ok := raw.(*UnifiedContext); ok {
			return uCtx
		}
	}
	return &UnifiedContext{
		RequestID:     GenerateUUID(),
		CorrelationID: GenerateUUID(),
		OccurredAt:    time.Now().UTC(),
	}
}
