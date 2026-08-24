package agent

import "strings"

// RefineOutcome keeps timeout/cancelled/error, but upgrades a false "success"
// when the official actions/runner aborted mid-job (OOM / SIGABRT / 134).
// Upstream run-helper.sh maps those unknown codes to exit 0.
func RefineOutcome(outcome, runnerLog string) string {
	if outcome != "success" {
		return outcome
	}
	if jobCompletedOK(runnerLog) {
		return "success"
	}
	if runnerLogIndicatesAbort(runnerLog) {
		return "failure"
	}
	return "success"
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
