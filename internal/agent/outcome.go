package agent

import "strings"

// RefineOutcome keeps timeout/cancelled/error, but upgrades a false "success"
// when the official actions/runner aborted mid-job (OOM / SIGABRT / 134)
// or wrote "completed with result: Failed". A missing success line is not
// enough: the host runner.log is often truncated. Guest remap covers that.
func RefineOutcome(outcome, runnerLog string) string {
	return RefineOutcomeForJob(outcome, runnerLog, "")
}

// RefineOutcomeForJob is RefineOutcome plus a check that the official runner
// accepted the assigned GitHub job. JIT configs are label-FIFO: a runner
// minted for "e2e" can be given an older queued "test" with the same labels.
func RefineOutcomeForJob(outcome, runnerLog, assignedName string) string {
	if started := runningJobName(runnerLog); assignedName != "" && started != "" &&
		!strings.EqualFold(started, assignedName) {
		return "error"
	}
	if outcome != "success" {
		return outcome
	}
	if jobCompletedOK(runnerLog) {
		return "success"
	}
	if jobCompletedFailed(runnerLog) || runnerLogIndicatesAbort(runnerLog) {
		return "failure"
	}
	// Missing "completed with result: succeeded" is not enough: the host
	// copy of runner.log is often truncated (mailbox unblocks before inject
	// copy). The guest already remaps a truly incomplete exit 0 to 98.
	return "success"
}

// RunningJobName is the last "Running job: NAME" line in an official runner log.
func RunningJobName(log string) string {
	return runningJobName(log)
}

func runningJobName(log string) string {
	const prefix = "running job:"
	low := strings.ToLower(log)
	idx := strings.LastIndex(low, prefix)
	if idx < 0 {
		return ""
	}
	rest := log[idx+len(prefix):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	return strings.TrimSpace(rest)
}

func jobCompletedOK(log string) bool {
	return strings.Contains(strings.ToLower(log), "completed with result: succeeded")
}

func jobCompletedFailed(log string) bool {
	low := strings.ToLower(log)
	return strings.Contains(low, "completed with result: failed") ||
		strings.Contains(low, "completed with result: cancelled") ||
		strings.Contains(low, "completed with result: canceled")
}

func runnerLogIndicatesAbort(log string) bool {
	low := strings.ToLower(log)
	if strings.Contains(low, "out of memory") {
		return true
	}
	if strings.Contains(low, "unknown error code: 134") {
		return true
	}
	if strings.Contains(low, "aborted") && strings.Contains(low, "runner.listener") {
		return true
	}
	return false
}
