package domain

// ReportMessageSummary carries only MSG-owned record counts. Total includes
// every message type, including activity and template rows; the directional
// counts include only their exact type, irrespective of sender or delivery.
type ReportMessageSummary struct {
	Total    int64
	Incoming int64
	Outgoing int64
}
