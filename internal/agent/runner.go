package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// RunnerStarter injects JIT and starts the guest actions/runner.
type RunnerStarter interface {
	// StartRunner attaches job material to the VM and starts the runner.
	// Implementations must not log JITConfig in full.
	StartRunner(ctx context.Context, id vmm.ID, job JobPayload) error
}

// StubRunner is a no-op runner starter for local testing and unit tests.
type StubRunner struct {
	Log *slog.Logger
	// StartFunc optionally overrides StartRunner (tests inject failures).
	StartFunc func(ctx context.Context, id vmm.ID, job JobPayload) error
}

// StartRunner logs a non-secret bind and returns nil unless StartFunc is set.
func (s *StubRunner) StartRunner(ctx context.Context, id vmm.ID, job JobPayload) error {
	if s.StartFunc != nil {
		return s.StartFunc(ctx, id, job)
	}
	if s.Log != nil {
		// Never log JIT material; only lengths / presence.
		jitLen := len(job.JITConfig)
		s.Log.Info("stub runner start",
			"vm_id", string(id),
			"job_id", job.JobID,
			"jit_present", jitLen > 0,
			"jit_bytes", jitLen,
		)
	}
	return nil
}

// InjectRunner writes JIT config via GuestExec and starts the official runner process.
//
// Guest command (production image):
//
//	$RUNNER_ROOT/run.sh --jitconfig <path-or-stdin>
//
// Fake/FileGuestExec records the exec without launching a real binary.
type InjectRunner struct {
	Guest GuestExec
	Log   *slog.Logger
	// RunnerPath is the guest-side path to the runner entrypoint.
	// Default: /opt/actions-runner/run.sh
	RunnerPath string
	// JITRelPath is the guest-relative path for the encoded JIT file.
	// Default: jitconfig
	JITRelPath string
}

// StartRunner injects EncodedJITConfig and invokes the runner entrypoint.
func (r *InjectRunner) StartRunner(ctx context.Context, id vmm.ID, job JobPayload) error {
	if r.Guest == nil {
		return fmt.Errorf("agent: InjectRunner.Guest is nil")
	}
	if job.JITConfig == "" {
		return fmt.Errorf("agent: JIT config required to start runner")
	}
	jitRel := r.JITRelPath
	if jitRel == "" {
		jitRel = "jitconfig"
	}
	runnerPath := r.RunnerPath
	if runnerPath == "" {
		runnerPath = "/opt/actions-runner/run.sh"
	}

	// Write secret with tight mode; never log contents.
	if err := r.Guest.WriteFile(ctx, id, jitRel, []byte(job.JITConfig), 0o600); err != nil {
		return fmt.Errorf("agent: write jit: %w", err)
	}

	// Also write a non-secret job meta file for operators/debug.
	meta := fmt.Sprintf("job_id=%s\nrunner_name=%s\n", job.JobID, job.RunnerName)
	_ = r.Guest.WriteFile(ctx, id, "job.meta", []byte(meta), 0o600)

	// Start runner with JIT. Official runner accepts --jitconfig <file>.
	// Path inside guest workspace for file backends: ./jitconfig relative to guest dir;
	// production images map this path or use absolute /run/temperci/jitconfig.
	jitArg := jitRel
	if r.Log != nil {
		r.Log.Info("starting guest runner",
			"vm_id", string(id),
			"job_id", job.JobID,
			"jit_present", true,
			"jit_bytes", len(job.JITConfig),
			"runner_path", runnerPath,
		)
	}
	if err := r.Guest.Exec(ctx, id, runnerPath, "--jitconfig", jitArg); err != nil {
		return fmt.Errorf("agent: guest exec runner: %w", err)
	}
	return nil
}
