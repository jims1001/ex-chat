package middleware

import (
	"strings"
	"testing"
)

func TestInvitationQueriesNeverLeakMailboxProof(t *testing.T) {
	for _, path := range []string{"/auth/invitations/activate", "/api/v1/agent_invitations/accept", "/app/auth/invitation", "/api/v1/accounts/1/agent_invitations"} {
		for _, query := range []string{"token=" + strings.Repeat("a", 64), "TOKEN=hidden&unknown=hidden", "token=%invalid"} {
			if actual := safeRequestQuery(path, query); actual != "" {
				t.Fatalf("invitation query persisted for %s", path)
			}
		}
	}
}
