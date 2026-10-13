package service

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

var errNumericIDProbe = errors.New("test owner probe")

type numericIDProbe struct{ seen uint }

func (p *numericIDProbe) FindMember(accountID, id uint) (*domain.AccountUser, error) {
	p.seen = id
	return nil, errNumericIDProbe
}
func TestAutomationLegacyNumericIDBoundedAndExact(t *testing.T) {
	db, _, _ := compatibilityTestDB(t)
	require.NoError(t, db.AutoMigrate(&domain.Conversation{}))
	conv := domain.Conversation{AccountID: 91, InboxID: 1, ContactID: 1, Status: "open", ObjectVersion: 1}
	require.NoError(t, db.Create(&conv).Error)
	probe := &numericIDProbe{}
	s := NewAutomationServiceWithOwnerPorts(db, repository.NewConversationRepository(db), nil, nil, probe, nil, nil)
	for _, token := range []string{"1", "1.0", "100e-2", "0.0001e4", "9007199254740993", "9.007199254740993e15", "18446744073709551615", "18446744073709551616", "0", "-1", "1.001", "1e-1"} {
		// The previous implementation is an oracle only for these tiny safe cases.
		r, ok := new(big.Rat).SetString(token)
		want := uint(0)
		if ok && r.IsInt() && r.Sign() > 0 && r.Num().BitLen() <= strconv.IntSize {
			want = uint(r.Num().Uint64())
		}
		probe.seen = 0
		err := s.executeAction(context.Background(), &conv, nil, ActionInstruction{ActionName: "assign_agent", ActionParams: []any{json.Number(token)}})
		require.Equal(t, want, probe.seen, token)
		if want > 0 {
			require.ErrorIs(t, err, errNumericIDProbe)
		} else {
			require.NoError(t, err)
		}
	}
	for _, token := range []string{"1e2147483647", "1e-2147483648", "1." + strings.Repeat("0", 1023)} {
		// Never pass an adversarial token to the previous Rat implementation.
		probe.seen = 0
		require.NoError(t, s.executeAction(context.Background(), &conv, nil, ActionInstruction{ActionName: "assign_agent", ActionParams: json.Number(token)}))
		require.Zero(t, probe.seen)
		probe.seen = 0
		require.ErrorIs(t, s.executeAction(context.Background(), &conv, nil, ActionInstruction{ActionName: "assign_agent", ActionParams: map[string]any{"user_id": json.Number(token), "agent_id": json.Number("7.000")}}), errNumericIDProbe)
		require.Equal(t, uint(7), probe.seen, "existing named fallback remains")
	}
}
