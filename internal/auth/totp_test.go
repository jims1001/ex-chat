package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMatchTOTPStepReturnsAcceptedCounter(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	at := time.Unix(1_700_000_010, 0).UTC()
	for _, offset := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		when := at.Add(offset)
		code, err := GenerateTOTPCode(secret, when)
		require.NoError(t, err)
		step, valid := MatchTOTPStep(secret, " "+code+" ", at)
		require.True(t, valid)
		require.Equal(t, when.Unix()/30, step)
	}
	for _, offset := range []time.Duration{-60 * time.Second, 60 * time.Second} {
		code, err := GenerateTOTPCode(secret, at.Add(offset))
		require.NoError(t, err)
		_, valid := MatchTOTPStep(secret, code, at)
		require.False(t, valid)
	}
}

func TestMatchTOTPStepRejectsMalformedInput(t *testing.T) {
	at := time.Unix(1_700_000_010, 0).UTC()
	for _, code := range []string{"", "12345", "1234567", "abcdef", "１２３４５６"} {
		_, valid := MatchTOTPStep("JBSWY3DPEHPK3PXP", code, at)
		require.False(t, valid)
	}
	for _, secret := range []string{"", "  ", "not a base32 secret!"} {
		_, valid := MatchTOTPStep(secret, "123456", at)
		require.False(t, valid)
	}
}
