package middleware_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type revocationCheck struct {
	repository.ChannelIAMRepository
	revoked bool
	err     error
}

func (r revocationCheck) IsTokenRevoked(string) (bool, error) { return r.revoked, r.err }

func TestVisitorRevocationFailsClosedAcrossCredentialLocations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, state := range []struct {
		name  string
		store revocationCheck
		want  int
	}{
		{"revoked", revocationCheck{revoked: true}, 401},
		{"store unavailable", revocationCheck{err: errors.New("DB unavailable")}, 503},
		{"not revoked", revocationCheck{}, 204},
	} {
		t.Run(state.name, func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.VisitorTokenRevocation(state.store))
			r.GET("/", func(c *gin.Context) { c.Status(204) })
			for _, loc := range []string{"visitor_session", "visitor_token", "X-Visitor-Session", "X-Visitor-Token"} {
				path := "/"
				if loc[0] != 'X' {
					path += "?" + loc + "=credential"
				}
				req := httptest.NewRequest(http.MethodGet, path, nil)
				if loc[0] == 'X' {
					req.Header.Set(loc, "credential")
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, state.want, w.Code, loc)
			}
		})
	}
}

func TestAgentRevocationLookupFailureDoesNotProceed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", middleware.AuthMiddleware("irrelevant", nil, revocationCheck{err: errors.New("DB unavailable")}), func(c *gin.Context) { t.Fatal("revocation error reached protected handler") })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer credential")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 500, w.Code)
}
