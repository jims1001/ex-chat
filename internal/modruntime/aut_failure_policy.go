package modruntime

import "github.com/OracleBetX-Projects/ex-chat/internal/domain"

const automationContinueDefinite = "continue_definite.v1"

func automationSameLogicalAction(a, b automationLocalAction) bool {
	return a.LogicalIndex != nil && b.LogicalIndex != nil && *a.LogicalIndex == *b.LogicalIndex
}
func automationResultIdentity(a automationLocalAction, result automationActionResult, index int) bool {
	return result.Index == index && result.Capability == a.Capability && result.Step == automationActionStep(a) && ((a.LogicalIndex == nil && result.LogicalIndex == nil) || (a.LogicalIndex != nil && result.LogicalIndex != nil && *a.LogicalIndex == *result.LogicalIndex))
}
func validAutomationRunnerPlan(c automationRunnerContext) bool {
	if !validAutomationActivityPolicy(c.ActivityPolicy) || (c.FailurePolicy != "" && c.FailurePolicy != automationContinueDefinite) {
		return false
	}
	if !validAutomationPlan(c.Actions) {
		return false
	}
	logical := -1
	for _, a := range c.Actions {
		if c.FailurePolicy == "" {
			if a.LogicalIndex != nil || a.FailureReason != "" {
				return false
			}
			continue
		}
		if a.LogicalIndex == nil || *a.LogicalIndex < 0 || *a.LogicalIndex > logical+1 || *a.LogicalIndex < logical {
			return false
		}
		logical = *a.LogicalIndex
	}
	return c.FailurePolicy == "" || (logical >= 0 && logical < 100 && len(c.Dispatches) == len(c.Actions))
}

// A failure or dependency skip is a no-effect proof, never a new owner version.
// The failed dispatch still carries the frozen identity and expected version.
func automationCheckpointStep(c automationRunnerContext, results []automationActionResult, index int, before uint) (uint, bool) {
	a, result := c.Actions[index], results[index]
	if !automationResultIdentity(a, result, index) {
		return 0, false
	}
	if result.Status == "succeeded" {
		for previous := index - 1; previous >= 0 && automationSameLogicalAction(c.Actions[previous], a); previous-- {
			if results[previous].Status != "succeeded" {
				return 0, false
			}
		}
		return automationResultAdvances(a, result, before, c.ActivityPolicy)
	}
	if c.FailurePolicy != automationContinueDefinite || result.ObjectVersion != 0 || result.ResourceID != 0 || result.Outcome != "" {
		return 0, false
	}
	switch result.Status {
	case "failed":
		if a.FailureReason != "" {
			return before, result.Reason == a.FailureReason
		}
		return before, result.Reason == "owner_command_rejected"
	case "skipped":
		if result.Reason != "dependency_failed" {
			return 0, false
		}
		for i := index - 1; i >= 0 && automationSameLogicalAction(c.Actions[i], a); i-- {
			if results[i].Status == "failed" {
				return before, true
			}
			if results[i].Status != "skipped" || results[i].Reason != "dependency_failed" {
				return 0, false
			}
		}
	}
	return 0, false
}

// Aggregate local outcomes. Successful preparation is a retained partial effect
// even when its dependent step failed. Skipping is never counted as success.
func automationCompletedStatus(results []automationActionResult) (string, string) {
	succeeded, failed, reason := false, false, ""
	for _, result := range results {
		succeeded = succeeded || result.Status == "succeeded"
		if result.Status == "failed" {
			failed = true
			if reason == "" {
				reason = result.Reason
			}
		}
	}
	if failed {
		if succeeded {
			return "partially_succeeded", reason
		}
		return "failed", reason
	}
	return "succeeded", ""
}

// Continuation checkpoints have a completed (successful/failed/dependency-skipped)
// prefix. Terminal cancellation alone may retain an unattempted skipped suffix.
func validAutomationContinuingCheckpoint(e domain.AutomationRuleExecution, c automationRunnerContext, results []automationActionResult, terminal bool, base uint) bool {
	if !validAutomationRunnerPlan(c) || len(results) != len(c.Actions) || e.RunnerNextAction < 0 || e.RunnerNextAction > len(results) {
		return false
	}
	if !terminal && e.RunnerNextAction == len(results) {
		return false
	}
	failedGroup := -1
	for i, a := range c.Actions {
		result := results[i]
		if *a.LogicalIndex == failedGroup && (result.Status != "skipped" || result.Reason != "dependency_failed") {
			return false
		}
		if result.Status == "failed" {
			failedGroup = *a.LogicalIndex
		}
		if result.Status == "succeeded" && result.Reason != "" {
			return false
		}
		if !automationResultIdentity(a, result, i) {
			return false
		}
		if i < e.RunnerNextAction {
			next, ok := automationCheckpointStep(c, results, i, base)
			if !ok || !validAutomationDispatch(c, e, results, i, base) {
				return false
			}
			base = next
		} else {
			if result.ObjectVersion != 0 || result.ResourceID != 0 || result.Outcome != "" {
				return false
			}
			if terminal {
				if e.Status != "cancelled" || result.Status != "skipped" || result.Reason != "cancelled_by_request" || !automationEmptyDispatch(c.Dispatches[i]) {
					return false
				}
			} else {
				if i > e.RunnerNextAction && (result.Status != "pending" || result.Reason != "" || !automationEmptyDispatch(c.Dispatches[i])) {
					return false
				}
				if i == e.RunnerNextAction {
					if result.Status != "pending" && result.Status != "accepted" && result.Status != "result_unknown" {
						return false
					}
					if result.Status == "pending" && (result.Reason != "" || !automationEmptyDispatch(c.Dispatches[i])) {
						return false
					}
					if result.Status != "pending" && (a.FailureReason != "" || !validAutomationDispatch(c, e, results, i, base)) {
						return false
					}
				}
			}
		}
	}
	if e.RunnerTargetVersion != base && !(e.RunnerNextAction == 0 && e.RunnerTargetVersion == e.TargetVersion) {
		return false
	}
	if terminal && e.Status != "cancelled" {
		status, reason := automationCompletedStatus(results)
		if e.RunnerNextAction != len(results) || e.Status != status || e.Error != reason {
			return false
		}
	}
	return true
}

// Infer only the historical base already carried by successful receipts or a
// frozen CON/MSG envelope; never inspect live owner state. Full causal checks
// subsequently compare it to the predecessor proof.
func automationCheckpointBase(e domain.AutomationRuleExecution, c automationRunnerContext, results []automationActionResult) uint {
	if e.RunnerEvaluation != "" {
		if evaluation, ok := decodeAutomationSiblingEvaluation(e); ok && evaluation.Version > 0 {
			return evaluation.Version
		}
	}
	for i := 0; i < e.RunnerNextAction; i++ {
		if c.Actions[i].Owner != "CUS" && !automationEmptyDispatch(c.Dispatches[i]) {
			return uint(c.Dispatches[i].Target.ExpectedVersion)
		}
		if results[i].Status == "succeeded" {
			return uint(results[i].ObjectVersion)
		}
	}
	return e.RunnerTargetVersion
}
