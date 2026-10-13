package modruntime

// Only new admissions opt into valid local no-op activity. Absence retains the
// historical receipt/envelope behavior across retry, deferred evaluation and restore.
const automationTouchValidActivity = "touch_valid.v1"
const automationActivityTouched = "activity_touched"

func validAutomationActivityPolicy(policy string) bool {
	return policy == "" || policy == automationTouchValidActivity
}

// This marker is part of the successful CON command receipt, not a live query
// field. It proves that the +1 effect was activity only and emitted no descendant.
type automationConversationActivityView struct {
	conversationView
	ActivityOnly bool `json:"activity_only,omitempty"`
}
