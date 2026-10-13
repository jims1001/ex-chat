package service

import (
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportHumanResponseDurations(t *testing.T) {
	start := time.Date(2024, 2, 29, 9, 0, 0, 0, time.UTC)
	message := func(offset time.Duration, kind, sender string, private bool) domain.Message {
		return domain.Message{CreatedAt: start.Add(offset), MessageType: kind, SenderType: sender, Private: private}
	}
	incoming := message(0, "incoming", "Contact", false)
	human := message(5*time.Minute, "outgoing", "User", false)
	check := func(t *testing.T, messages []domain.Message, wantFRT float64, wantOK bool, wantReplies []float64) {
		t.Helper()
		// The conversation timestamp must not replace the existing incoming start.
		conv := domain.Conversation{CreatedAt: start.Add(-24 * time.Hour)}
		frt, ok := CalculateConversationFRT(conv, messages, nil, false)
		assert.Equal(t, wantOK, ok)
		assert.Equal(t, wantFRT, frt)
		assert.Equal(t, wantReplies, CalculateConversationReplyTimes(messages, nil, false))
	}
	for _, sender := range []string{"AgentBot", "CaptainAssistant", "Captain::Assistant"} {
		t.Run(sender, func(t *testing.T) {
			bot := message(time.Minute, "outgoing", sender, false)
			t.Run("human_after_bot", func(t *testing.T) {
				check(t, []domain.Message{incoming, bot, human}, 300, true, []float64{300})
			})
			t.Run("bot_only", func(t *testing.T) {
				check(t, []domain.Message{incoming, bot}, 0, false, nil)
			})
		})
	}
	t.Run("multiple_bots_preserve_two_wait_cycles", func(t *testing.T) {
		messages := []domain.Message{
			incoming,
			message(time.Minute, "outgoing", "AgentBot", false),
			message(2*time.Minute, "incoming", "Contact", false),
			message(3*time.Minute, "outgoing", "CaptainAssistant", false),
			message(4*time.Minute, "outgoing", "Captain::Assistant", false),
			human,
			message(6*time.Minute, "outgoing", "User", false),
			message(10*time.Minute, "incoming", "Contact", false),
			message(11*time.Minute, "outgoing", "Captain::Assistant", false),
			message(12*time.Minute, "outgoing", "User", true),
			message(13*time.Minute, "incoming", "Contact", false),
			message(15*time.Minute, "outgoing", "User", false),
		}
		check(t, messages, 300, true, []float64{300, 300})
	})
	t.Run("multiple_bots_only", func(t *testing.T) {
		check(t, []domain.Message{incoming,
			message(time.Minute, "outgoing", "AgentBot", false),
			message(2*time.Minute, "outgoing", "CaptainAssistant", false),
			message(3*time.Minute, "outgoing", "Captain::Assistant", false),
		}, 0, false, nil)
	})
	t.Run("historical_sender_compatibility", func(t *testing.T) {
		for _, sender := range []string{"User", "", "LegacySender", "Contact", "agentbot", "AgentBot ", "captainassistant", "Captain::Assistant "} {
			reply := human
			reply.SenderType = sender
			check(t, []domain.Message{incoming, reply}, 300, true, []float64{300})
		}
	})
	t.Run("delivery_status_compatibility", func(t *testing.T) {
		for _, status := range []string{"", "sent", "saved", "queued", "failed", "delivered", "read", "unknown"} {
			reply := human
			reply.Status = status
			reply.DeliveryFailed = true
			check(t, []domain.Message{incoming, reply}, 300, true, []float64{300})
			for _, sender := range []string{"AgentBot", "CaptainAssistant", "Captain::Assistant"} {
				bot := message(time.Minute, "outgoing", sender, false)
				bot.Status = status
				check(t, []domain.Message{incoming, bot, reply}, 300, true, []float64{300})
			}
		}
	})
	t.Run("private_and_non_outgoing_do_not_end_wait", func(t *testing.T) {
		messages := []domain.Message{
			message(-time.Minute, "outgoing", "User", false), incoming,
			message(time.Minute, "outgoing", "User", true),
			message(2*time.Minute, "activity", "User", false),
			message(3*time.Minute, "template", "User", false),
			message(4*time.Minute, "Outgoing", "User", false), human,
		}
		check(t, messages, 300, true, []float64{300})
		check(t, messages[:6], 0, false, nil)
	})
	t.Run("incoming_start_compatibility", func(t *testing.T) {
		// The bounded change does not reinterpret incoming privacy/sender history.
		startMessage := incoming
		startMessage.Private, startMessage.SenderType = true, "AgentBot"
		check(t, []domain.Message{startMessage, human}, 300, true, []float64{300})
		check(t, []domain.Message{human}, 0, false, nil)
		check(t, nil, 0, false, nil)
		check(t, []domain.Message{incoming, message(0, "outgoing", "User", false)}, 0, true, []float64{0})
	})
	t.Run("resolution_fcr_and_bot_contracts_unchanged", func(t *testing.T) {
		conv := domain.Conversation{ID: 1, Status: domain.ConversationStatusResolved, CreatedAt: start, UpdatedAt: start.Add(time.Hour)}
		bot := message(time.Minute, "outgoing", "AgentBot", false)
		frt, ok := CalculateConversationFRT(conv, []domain.Message{incoming, bot}, nil, false)
		require.False(t, ok)
		require.Zero(t, frt)
		resolution, ok := CalculateConversationResolutionTime(conv, nil, false)
		require.True(t, ok)
		require.Equal(t, float64(3600), resolution)
		fcr, resolved, handoff := CalculateFCRAndBotMetrics([]domain.Conversation{conv}, map[uint][]domain.Message{1: {incoming, bot}})
		require.Equal(t, float64(100), fcr, "FCR retains its existing public-outgoing rule")
		require.Equal(t, int64(1), resolved)
		require.Zero(t, handoff)
	})
}

func TestReportHumanResponseBusinessHours(t *testing.T) {
	location := time.FixedZone("inbox", 8*60*60)
	start := time.Date(2024, 2, 29, 16, 0, 0, 0, location)
	end := start.Add(18 * time.Hour)
	inbox := &domain.Inbox{Timezone: "Asia/Shanghai", WorkingHoursEnabled: true, WorkingHours: `[{"day_of_week":4,"open_hour":9,"close_hour":17},{"day_of_week":5,"open_hour":9,"close_hour":17}]`}
	for _, sender := range []string{"AgentBot", "CaptainAssistant", "Captain::Assistant"} {
		t.Run(sender, func(t *testing.T) {
			messages := []domain.Message{
				{CreatedAt: start, MessageType: "incoming", SenderType: "Contact"},
				{CreatedAt: start.Add(5 * time.Minute), MessageType: "outgoing", SenderType: sender},
				{CreatedAt: end, MessageType: "outgoing", SenderType: "User"},
			}
			for _, tc := range []struct {
				business bool
				want     float64
			}{{false, 64800}, {true, 7200}} {
				frt, ok := CalculateConversationFRT(domain.Conversation{}, messages, inbox, tc.business)
				require.True(t, ok)
				require.Equal(t, tc.want, frt)
				require.Equal(t, []float64{tc.want}, CalculateConversationReplyTimes(messages, inbox, tc.business))
			}
		})
	}
}
