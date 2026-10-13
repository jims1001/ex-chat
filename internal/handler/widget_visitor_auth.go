package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// requireWidgetVisitor is the authorization boundary shared by old and new
// widget HTTP handlers. Public source identifiers are never credentials. Every
// session is bound to its inbox, tenant, and the current persisted contact map.
func requireWidgetVisitor(c *gin.Context, inbox *domain.Inbox, contacts *repository.ContactRepository, secret string) (*widgetVisitorIdentity, error) {
	claims, err := auth.ParseVisitorToken(visitorSessionToken(c), secret)
	if err != nil || inbox == nil || contacts == nil || claims.InboxID != inbox.ID {
		response.Forbidden(c, "A valid visitor session for this inbox is required")
		return nil, auth.ErrInvalidVisitorToken
	}
	contact, err := contacts.FindContactBySourceID(inbox.ID, claims.SourceID)
	if err != nil || contact == nil || contact.ID != claims.ContactID || contact.AccountID != inbox.AccountID {
		response.Forbidden(c, "Visitor session does not match this profile")
		return nil, auth.ErrInvalidVisitorToken
	}
	identity := &widgetVisitorIdentity{claims: claims, contact: contact}
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), widgetVisitorIdentityKey{}, identity))
	return identity, nil
}

// validateVisitorIdentityUpdate prevents profile edits from claiming another
// persisted identity. A session may update only its already-owned profile;
// merging/recovering a different contact requires a separate authenticated flow.
func validateVisitorIdentityUpdate(c *gin.Context, contacts *repository.ContactRepository, contact *domain.Contact, identifier, email string) bool {
	for _, lookup := range []struct {
		value string
		find  func(uint, string) (*domain.Contact, error)
	}{{strings.TrimSpace(identifier), contacts.FindByIdentifier}, {strings.ToLower(strings.TrimSpace(email)), contacts.FindByEmail}} {
		if lookup.value == "" {
			continue
		}
		existing, err := lookup.find(contact.AccountID, lookup.value)
		if err != nil {
			response.InternalError(c, "Failed to validate visitor identity")
			return false
		}
		if existing != nil && existing.ID != contact.ID {
			response.Forbidden(c, "Identity is not owned by this visitor session")
			return false
		}
	}
	return true
}

// validateWidgetIdentifierProof applies the same inbox HMAC policy to every
// visitor route that can change an external identifier.
func validateWidgetIdentifierProof(c *gin.Context, inbox *domain.Inbox, identifier, identifierHash string) bool {
	identifier = strings.TrimSpace(identifier)
	identifierHash = strings.TrimSpace(identifierHash)
	if identifier == "" {
		return true
	}
	if identifierHash == "" {
		if inbox.HMACMandatory {
			response.Error(c, http.StatusUnauthorized, "identifier_hash is mandatory for this inbox")
			return false
		}
		return true
	}
	if inbox.HMACToken == "" {
		response.Error(c, http.StatusUnauthorized, "Identity verification is not configured")
		return false
	}
	mac := hmac.New(sha256.New, []byte(inbox.HMACToken))
	_, _ = mac.Write([]byte(identifier))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(strings.ToLower(identifierHash))) {
		response.Error(c, http.StatusUnauthorized, "Invalid identifier_hash")
		return false
	}
	return true
}
