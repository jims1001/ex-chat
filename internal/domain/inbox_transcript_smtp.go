package domain

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const InboxSMTPBindingKind = "inbox_smtp_v1"
const InboxSMTPFingerprintPrefix = "inbox_smtp_v1:sha256:"

// InboxSMTPBinding is private, password-free evidence of the CHN route selected
// at transcript admission. IDs are strings so JSON cannot round uint64 values.
// CreatedAt identifies the Inbox incarnation, rather than its last configuration
// update. Fingerprint excludes credentials and unrelated provider fields.
type InboxSMTPBinding struct {
	Kind        string `json:"kind"`
	AccountID   string `json:"account_id"`
	InboxID     string `json:"inbox_id"`
	CreatedAt   string `json:"created_at"`
	Fingerprint string `json:"fingerprint"`
}

// InboxTranscriptSMTP is a local CHN owner port. MSG retains the immutable
// transcript and its durable marker; CHN alone resolves live SMTP credentials.
// Implementations must recheck the binding immediately before beforeSubmit.
// This check and MSG's marker are not a transaction across owners: changes after
// the check cannot retract DATA that has already become eligible for submission.
type InboxTranscriptSMTP interface {
	SendInboxTranscript(ctx context.Context, binding InboxSMTPBinding, from, to, subject, htmlBody, textBody string, beforeSubmit func() error) OrdinarySMTPResult
}

// ValidateInboxSMTPBinding validates proof syntax only. It cannot prove that a
// current Inbox exists or still matches; the CHN transport checks that on every
// safe attempt and again before the caller's durable submission marker.
func ValidateInboxSMTPBinding(binding InboxSMTPBinding) error {
	invalid := errors.New("invalid inbox smtp binding")
	if binding.Kind != InboxSMTPBindingKind {
		return invalid
	}
	for _, raw := range []string{binding.AccountID, binding.InboxID} {
		value, err := strconv.ParseUint(raw, 10, strconv.IntSize)
		if err != nil || value == 0 || strconv.FormatUint(value, 10) != raw {
			return invalid
		}
	}
	created, err := time.Parse(time.RFC3339Nano, binding.CreatedAt)
	if err != nil || created.IsZero() || created.UTC().Format(time.RFC3339Nano) != binding.CreatedAt {
		return invalid
	}
	if !strings.HasPrefix(binding.Fingerprint, InboxSMTPFingerprintPrefix) {
		return invalid
	}
	digest := strings.TrimPrefix(binding.Fingerprint, InboxSMTPFingerprintPrefix)
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest {
		return invalid
	}
	return nil
}
