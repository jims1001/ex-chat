package channel

import "context"

// TextIntent is the bounded send input. MSG retains message/contact identity,
// atomic admission, claims and the dispatch fence; CHN owns provider credentials.
type TextIntent struct {
	AttemptID   string
	AccountID   uint
	InboxID     uint
	RecipientID string
	Body        string
}

// TextBinding contains only the non-secret fields already persisted by MSG.
// ConfigFingerprint includes the same encrypted-connection/configuration hash
// used before the owner split; it cannot be used to retrieve credentials.
type TextBinding struct {
	ConnectionID      uint
	Provider          string
	ProviderAccountID string
	SenderID          string
	ConfigFingerprint string
	APIVersion        string
	CallbackURL       string
}

type TextSnapshot struct {
	TextIntent
	TextBinding
}

// PreparedText is an opaque, already resolved transport operation. Preparation
// does no provider I/O. MSG must persist StartOutboundDispatch before Send.
// Send performs no new owner read: rotation after preparation cannot recall an
// in-flight request, matching the pre-existing dispatch race boundary.
type PreparedText interface {
	Send(context.Context) Result
}

// TextOwner is a local CHN capability, not a remote credential API or a claim of
// physical process isolation. None of its results contain credentials, keys,
// encrypted connections, provider requests or unrestricted storage handles.
type TextOwner interface {
	Ready() error
	OutboundEnabled() bool
	PrepareText(context.Context, TextIntent) (TextBinding, error)
	PrepareDispatch(context.Context, TextSnapshot) (PreparedText, error)
	LookupText(context.Context, TextSnapshot, string) (Result, error)
	VerifyTwilioStatus(context.Context, TextSnapshot, StatusEvent, map[string]string, string) error
	ParseWhatsAppStatuses(uint, string, []byte) ([]StatusEvent, error)
	CheckBinding(context.Context, TextSnapshot) error
}
