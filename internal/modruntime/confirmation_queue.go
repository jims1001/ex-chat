package modruntime

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"strings"
)

// Confirmation bearer bodies are never ordinary queue data. This narrow guard
// is applied at admission before legacy receipt replay and again in the handler.
// It deliberately does not inspect arbitrary text or change other email types.
func confirmationQueueRestriction(command foundation.CommandEnvelope) *ContractFailure {
	emailType, _ := command.Input["email_type"].(string)
	if command.CommandType == "msg.email.send.v1" && strings.EqualFold(strings.TrimSpace(emailType), "confirmation") {
		return &ContractFailure{Type: "validation_failed", Message: "confirmation email requires dedicated direct submission"}
	}
	return nil
}
