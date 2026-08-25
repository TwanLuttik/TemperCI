package agent

import "testing"

func TestRefineOutcome_OOMLogIsFailure(t *testing.T) {
	log := "\n√ Connected to GitHub\n\nCurrent runner version: '2.336.0'\n" +
		"2026-08-24 03:36:39Z: Running job: e2e\n" +
		"Out of memory.\n" +
		"/opt/actions-runner/run-helper.sh: line 36: 2112901 Aborted                 \"$DIR\"/bin/Runner.Listener run $*\n" +
		"Exiting with unknown error code: 134\n" +
		"Exiting runner...\n"
	if got := RefineOutcome("success", log); got != "failure" {
		t.Fatalf("RefineOutcome = %q want failure", got)
	}
}

func TestRefineOutcome_CompletedJobStaysSuccess(t *testing.T) {
	log := "2026-08-24 03:36:39Z: Running job: test\n" +
		"2026-08-24 03:41:13Z: Job test completed with result: Succeeded\n" +
		"Runner listener exit with 0 return code, stop the service, no retry needed.\n"
	if got := RefineOutcome("success", log); got != "success" {
		t.Fatalf("RefineOutcome = %q want success", got)
	}
}

func TestRefineOutcome_KeepsNonSuccess(t *testing.T) {
	if got := RefineOutcome("timeout", "Out of memory.\n"); got != "timeout" {
		t.Fatalf("RefineOutcome = %q want timeout", got)
	}
	if got := RefineOutcome("cancelled", ""); got != "cancelled" {
		t.Fatalf("RefineOutcome = %q want cancelled", got)
	}
	if got := RefineOutcome("failure", "Job x completed with result: Failed\n"); got != "failure" {
		t.Fatalf("RefineOutcome = %q want failure", got)
	}
}

func TestRefineOutcome_StartedJobWithoutCompletionIsFailure(t *testing.T) {
	log := "\n√ Connected to GitHub\n\nCurrent runner version: '2.336.0'\n" +
		"2026-08-24 07:58:17Z: Listening for Jobs\n" +
		"2026-08-24 07:58:20Z: Running job: e2e\n"
	if got := RefineOutcome("success", log); got != "failure" {
		t.Fatalf("RefineOutcome = %q want failure (incomplete job)", got)
	}
}

func TestRefineOutcome_FailedCompletionIsFailure(t *testing.T) {
	log := "2026-08-24 03:36:39Z: Running job: e2e\n" +
		"2026-08-24 03:41:13Z: Job e2e completed with result: Failed\n"
	if got := RefineOutcome("success", log); got != "failure" {
		t.Fatalf("RefineOutcome = %q want failure (Failed completion)", got)
	}
}

func TestRefineOutcomeForJob_DifferentJobIsError(t *testing.T) {
	log := "2026-08-24 22:16:18Z: Running job: test\n" +
		"2026-08-24 22:21:13Z: Job test completed with result: Succeeded\n"
	if got := RefineOutcomeForJob("success", log, "e2e"); got != "error" {
		t.Fatalf("RefineOutcomeForJob = %q want error (runner took test, assigned e2e)", got)
	}
}

func TestRefineOutcomeForJob_MatchingNameStaysSuccess(t *testing.T) {
	log := "2026-08-24 22:16:18Z: Running job: e2e\n" +
		"2026-08-24 22:21:13Z: Job e2e completed with result: Succeeded\n"
	if got := RefineOutcomeForJob("success", log, "e2e"); got != "success" {
		t.Fatalf("RefineOutcomeForJob = %q want success", got)
	}
}

func TestRunningJobName(t *testing.T) {
	if got := runningJobName("Running job: test\n"); got != "test" {
		t.Fatalf("runningJobName = %q want test", got)
	}
	if got := runningJobName("no job line"); got != "" {
		t.Fatalf("runningJobName = %q want empty", got)
	}
}

func TestRefineOutcome_ListeningOnlyStillSuccessUntilJobStarts(t *testing.T) {
	// Deprecated-runner / never-accepted-job stays the guest's 95 path.
	// Host RefineOutcome only upgrades once a job actually started.
	log := "\n√ Connected to GitHub\n2026-08-24 07:58:17Z: Listening for Jobs\n"
	if got := RefineOutcome("success", log); got != "success" {
		t.Fatalf("RefineOutcome = %q want success (no job started)", got)
	}
}
