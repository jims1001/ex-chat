package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCustomFilterRESTRejectsContradictoryContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		change func(*gin.Context)
		status int
	}{
		{"missing user id", func(c *gin.Context) { delete(c.Keys, middleware.ContextUserID) }, 401},
		{"zero user id", func(c *gin.Context) { c.Set(middleware.ContextUserID, uint(0)) }, 401},
		{"wrong user id type", func(c *gin.Context) { c.Set(middleware.ContextUserID, "7") }, 401},
		{"missing user", func(c *gin.Context) { delete(c.Keys, middleware.ContextUser) }, 401},
		{"typed nil user", func(c *gin.Context) { c.Set(middleware.ContextUser, (*domain.User)(nil)) }, 401},
		{"different authenticated user", func(c *gin.Context) { c.Set(middleware.ContextUser, &domain.User{ID: 8}) }, 401},
		{"missing account id", func(c *gin.Context) { delete(c.Keys, middleware.ContextAccountID) }, 403},
		{"different account id", func(c *gin.Context) { c.Set(middleware.ContextAccountID, uint(92)) }, 403},
		{"missing account", func(c *gin.Context) { delete(c.Keys, middleware.ContextAccount) }, 403},
		{"different account", func(c *gin.Context) { c.Set(middleware.ContextAccount, &domain.Account{ID: 92}) }, 403},
		{"missing membership", func(c *gin.Context) { delete(c.Keys, middleware.ContextAccountMembership) }, 403},
		{"typed nil membership", func(c *gin.Context) { c.Set(middleware.ContextAccountMembership, (*domain.AccountUser)(nil)) }, 403},
		{"different membership user", func(c *gin.Context) {
			c.Set(middleware.ContextAccountMembership, &domain.AccountUser{AccountID: 91, UserID: 8, Role: domain.RoleAdministrator})
		}, 403},
		{"different membership account", func(c *gin.Context) {
			c.Set(middleware.ContextAccountMembership, &domain.AccountUser{AccountID: 92, UserID: 7, Role: domain.RoleAdministrator})
		}, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A nil repository proves rejection happens before any persistence.
			h := &AdvancedHandler{}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(middleware.ContextUserID, uint(7))
				c.Set(middleware.ContextUser, &domain.User{ID: 7})
				c.Set(middleware.ContextAccountID, uint(91))
				c.Set(middleware.ContextAccount, &domain.Account{ID: 91})
				c.Set(middleware.ContextAccountMembership, &domain.AccountUser{AccountID: 91, UserID: 7, Role: domain.RoleAdministrator})
				tc.change(c)
			})
			r.GET("/:account_id", h.ListCustomFilters)
			r.POST("/:account_id", h.CreateCustomFilter)
			r.GET("/:account_id/:id", h.GetCustomFilter)
			r.PUT("/:account_id/:id", h.UpdateCustomFilter)
			r.PATCH("/:account_id/:id", h.UpdateCustomFilter)
			r.DELETE("/:account_id/:id", h.DeleteCustomFilter)
			for _, route := range []struct{ method, path string }{{"GET", "/91"}, {"POST", "/91"}, {"GET", "/91/1"}, {"PUT", "/91/1"}, {"PATCH", "/91/1"}, {"DELETE", "/91/1"}} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, strings.NewReader(`{"name":"private","filter_type":"contact","query":{}}`)))
				require.Equal(t, tc.status, w.Code, route.method+route.path)
			}
		})
	}
}

func TestCustomFilterRESTInvalidIdentifiersFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdvancedHandler{}
	for _, path := range []string{"/0/1", "/-1/1", "/not-an-account/1", "/18446744073709551616/1", "/091/1", "/91/0", "/91/-1", "/91/not-an-id", "/91/18446744073709551616", "/91/01"} {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(middleware.ContextUserID, uint(7))
			c.Set(middleware.ContextUser, &domain.User{ID: 7})
			c.Set(middleware.ContextAccountID, uint(91))
			c.Set(middleware.ContextAccount, &domain.Account{ID: 91})
			c.Set(middleware.ContextAccountMembership, &domain.AccountUser{AccountID: 91, UserID: 7})
		})
		r.GET("/:account_id/:id", h.GetCustomFilter)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, w.Code, path)
	}
}
