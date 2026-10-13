package service

import "errors"

// ErrCampaignPrerequisiteRead distinguishes unavailable audience/deduplication
// reads from business rejections. Such failures occur before dispatch effects.
var ErrCampaignPrerequisiteRead = errors.New("campaign prerequisite read failed")

// Keep diagnostics bounded while preserving the dependency cause for callers
// using errors.Is/As. Do not expose database text or audience/contact data.
type campaignPrerequisiteReadError struct {
	cause error
}

func (campaignPrerequisiteReadError) Error() string   { return ErrCampaignPrerequisiteRead.Error() }
func (e campaignPrerequisiteReadError) Unwrap() error { return e.cause }
func (campaignPrerequisiteReadError) Is(target error) bool {
	return target == ErrCampaignPrerequisiteRead
}
