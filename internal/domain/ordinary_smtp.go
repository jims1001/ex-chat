package domain

// OrdinarySMTPOutcome describes submission evidence, never mailbox delivery.
// Only a proven pre-submission transient failure is eligible for automatic retry.
type OrdinarySMTPOutcome string

const (
	OrdinarySMTPAccepted                  OrdinarySMTPOutcome = "accepted"
	OrdinarySMTPPreSubmissionTransient    OrdinarySMTPOutcome = "proven_pre_submission_transient"
	OrdinarySMTPPermanentFailure          OrdinarySMTPOutcome = "permanent_failure"
	OrdinarySMTPAuthenticationFailure     OrdinarySMTPOutcome = "authentication_failure"
	OrdinarySMTPCancelledBeforeSubmission OrdinarySMTPOutcome = "cancelled_before_submission"
	OrdinarySMTPUnknown                   OrdinarySMTPOutcome = "unknown"
)

// OrdinarySMTPResult carries no provider text, address, body, or credential.
type OrdinarySMTPResult struct {
	Outcome OrdinarySMTPOutcome
}
