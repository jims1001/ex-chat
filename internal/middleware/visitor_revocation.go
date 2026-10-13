package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type visitorRevocationStore interface{ IsTokenRevoked(string) (bool, error) }

// VisitorTokenRevocation is shared by bootstrap, widget and public visitor HTTP
// routes. Purpose/scope verification stays at each handler boundary. Every
// supplied credential is checked so a revoked secondary credential cannot be
// silently ignored in favor of another query/header spelling.
func VisitorTokenRevocation(store visitorRevocationStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokens := append([]string{}, c.QueryArray("visitor_session")...)
		tokens = append(tokens, c.QueryArray("visitor_token")...)
		tokens = append(tokens, c.Request.Header.Values("X-Visitor-Session")...)
		tokens = append(tokens, c.Request.Header.Values("X-Visitor-Token")...)
		for _, token := range tokens {
			if token == "" {
				continue
			} // the handler rejects explicit-empty credentials
			if len(token) > 16384 {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid visitor credential"})
				return
			}
			if store == nil {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "visitor authentication unavailable"})
				return
			}
			revoked, err := store.IsTokenRevoked(token)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "visitor authentication unavailable"})
				return
			}
			if revoked {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "visitor session revoked"})
				return
			}
		}
		c.Next()
	}
}
