package service

import "github.com/OracleBetX-Projects/ex-chat/internal/domain"

// slaFacts is one successful evaluation snapshot. It is deliberately local to
// the call: a failed owner read must not become an empty timeline or be repaired
// by a later read after writes have started.
type slaFacts struct {
	policy      domain.SLAPolicy
	inbox       *domain.Inbox
	loadedInbox *domain.Inbox
	messages    []domain.Message
	breaches    map[string]int64
}

func (s *SLAService) loadSLAFacts(conv *domain.Conversation) (slaFacts, error) {
	var facts slaFacts
	var policies []domain.SLAPolicy
	if err := s.db.Where("account_id = ?", conv.AccountID).Find(&policies).Error; err != nil {
		return slaFacts{}, err
	}
	facts.policy = s.resolvePolicy(conv, policies)
	if facts.policy.ID == 0 {
		return facts, nil
	}
	inbox, err := s.loadInbox(conv)
	if err != nil {
		return slaFacts{}, err
	}
	facts.loadedInbox = inbox
	if facts.policy.OnlyDuringBusinessHours {
		facts.inbox = inbox
	}
	facts.messages, err = s.loadMessages(conv)
	if err != nil {
		return slaFacts{}, err
	}
	facts.breaches = make(map[string]int64)
	for _, metric := range []struct {
		kind      string
		threshold int
	}{
		{"first_response", facts.policy.FirstResponseTimeThreshold},
		{"next_response", facts.policy.NextResponseTimeThreshold},
		{"resolution", facts.policy.ResolutionTimeThreshold},
	} {
		if metric.threshold <= 0 {
			continue
		}
		var count int64
		if err := s.db.Model(&domain.SLABreachLog{}).
			Where("account_id = ? AND conversation_id = ? AND breach_type = ?", conv.AccountID, conv.ID, metric.kind).
			Count(&count).Error; err != nil {
			return slaFacts{}, err
		}
		facts.breaches[metric.kind] = count
	}
	return facts, nil
}
