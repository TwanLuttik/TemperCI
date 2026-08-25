package agent

import "strings"

// RefineOutcome keeps timeout/cancelled/error, but upgrades a false "success"
// when the official actions/runner aborted mid-job (OOM / SIGABRT / 134)
// or started a job and never wrote "completed with result: succeeded".
// Upstream run-helper.sh maps those unknown codes to exit 0.
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
	if runnerLogIndicatesAbort(runnerLog) || jobStartedButIncomplete(runnerLog) {
		return "failure"
	}
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

func jobStartedButIncomplete(log string) bool {
	low := strings.ToLower(log)
	return strings.Contains(low, "running job:") &&
		!strings.Contains(low, "completed with result: succeeded")
}

func jobCompletedOK(log string) bool {
	return strings.Contains(strings.ToLower(log), "completed with result: succeeded")
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
