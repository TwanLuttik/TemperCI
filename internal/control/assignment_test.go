package control

import (
	"context"
	"testing"
	"time"
)

func TestAssignmentStore_GetByRunnerName(t *testing.T) {
	s := NewAssignmentStore()
	s.Put(&Assignment{JobID: 10, Status: AssignmentStarted, RunnerName: "temperci-job-10"})
	got := s.GetByRunnerName("temperci-job-10")
	if got == nil || got.JobID != 10 {
		t.Fatalf("GetByRunnerName = %+v", got)
	}
	if s.GetByRunnerName("missing") != nil {
		t.Fatal("expected nil for unknown runner")
	}
}

func TestAssignmentStore_ClaimNextFIFO(t *testing.T) {
	s := NewAssignmentStore()
	s.Put(&Assignment{JobID: 1, Status: AssignmentMinted, EncodedJITConfig: "jit-1"})
	s.Put(&Assignment{JobID: 2, Status: AssignmentMinted, EncodedJITConfig: "jit-2"})

	if s.PendingLen() != 2 {
		t.Fatalf("pending=%d", s.PendingLen())
	}

	a1 := s.ClaimNext("agent-a", nil)
	if a1 == nil || a1.JobID != 1 {
		t.Fatalf("first claim = %+v", a1)
	}
	if a1.Status != AssignmentAssigned || a1.AssignedAgentID != "agent-a" {
		t.Fatalf("first claim state = %+v", a1)
	}
	if a1.EncodedJITConfig != "jit-1" {
		t.Fatal("jit missing on claim")
	}

	a2 := s.ClaimNext("agent-a", nil)
	if a2 == nil || a2.JobID != 2 {
		t.Fatalf("second claim = %+v", a2)
	}
	if s.ClaimNext("agent-a", nil) != nil {
		t.Fatal("expected empty queue")
	}
	if s.PendingLen() != 0 {
		t.Fatalf("pending after drain = %d", s.PendingLen())
	}
}

func TestAssignmentStore_Lifecycle(t *testing.T) {
	s := NewAssignmentStore()
	s.Put(&Assignment{JobID: 10, Status: AssignmentMinted, EncodedJITConfig: "secret"})
	claimed := s.ClaimNext("host-1", nil)
	if claimed == nil {
		t.Fatal("expected claim")
	}
	if err := s.MarkStarted(10, "host-1", "vm-abc", true); err != nil {
		t.Fatal(err)
	}
	got := s.Get(10)
	if got.Status != AssignmentStarted || !got.WarmBind || got.VMID != "vm-abc" {
		t.Fatalf("started = %+v", got)
	}
	if err := s.MarkFinished(10, "host-1", "success", "vm-abc", true, ""); err != nil {
		t.Fatal(err)
	}
	got = s.Get(10)
	if got.Status != AssignmentFinished || got.Outcome != "success" {
		t.Fatalf("finished = %+v", got)
	}
	if got.EncodedJITConfig != "" {
		t.Fatal("expected JIT cleared after finish")
	}
}

func TestAssignmentStore_WrongAgentRejected(t *testing.T) {
	s := NewAssignmentStore()
	s.Put(&Assignment{JobID: 5, Status: AssignmentMinted})
	_ = s.ClaimNext("agent-a", nil)
	if err := s.MarkStarted(5, "agent-b", "vm-1", false); err == nil {
		t.Fatal("expected wrong-agent error")
	}
}

func TestAssignmentStore_CancelStartedAndMinted(t *testing.T) {
	s := NewAssignmentStore()
	s.Put(&Assignment{JobID: 1, Status: AssignmentMinted, EncodedJITConfig: "jit"})
	if err := s.Cancel(1, "operator cancel"); err != nil {
		t.Fatal(err)
	}
	got := s.Get(1)
	if got.Status != AssignmentFinished || got.Outcome != "cancelled" {
		t.Fatalf("minted cancel = %+v", got)
	}
	if got.EncodedJITConfig != "" || s.PendingLen() != 0 {
		t.Fatalf("jit/pending after cancel: jit=%q pending=%d", got.EncodedJITConfig, s.PendingLen())
	}
	if err := s.Cancel(1, "again"); err != nil {
		t.Fatal(err)
	}

	s.Put(&Assignment{JobID: 2, Status: AssignmentMinted})
	_ = s.ClaimNext("host-1", nil)
	_ = s.MarkStarted(2, "host-1", "vm-1", true)
	if err := s.Cancel(2, "kill vm"); err != nil {
		t.Fatal(err)
	}
	got = s.Get(2)
	if got.Status != AssignmentFinished || got.Outcome != "cancelled" || got.VMID != "vm-1" {
		t.Fatalf("started cancel = %+v", got)
	}

	s.Put(&Assignment{JobID: 3, Status: AssignmentFinished, Outcome: "success"})
	if err := s.Cancel(3, "late"); err == nil {
		t.Fatal("expected error cancelling a finished success job")
	}
}

func TestAssignmentStore_FailedNotClaimable(t *testing.T) {
	s := NewAssignmentStore()
	s.Put(&Assignment{JobID: 9, Status: AssignmentFailed, Error: "mint"})
	if s.ClaimNext("a", nil) != nil {
		t.Fatal("failed job should not be claimed")
	}
}

func TestAssignmentStore_ClaimNextPrefersCachedRepo(t *testing.T) {
	s := NewAssignmentStore()
	s.Put(&Assignment{JobID: 1, Status: AssignmentMinted, RepoFullName: "acme/old", EncodedJITConfig: "a"})
	s.Put(&Assignment{JobID: 2, Status: AssignmentMinted, RepoFullName: "acme/hot", EncodedJITConfig: "b"})

	got := s.ClaimNext("host-1", []string{"acme/hot"})
	if got == nil || got.JobID != 2 {
		t.Fatalf("sticky claim = %+v want job 2", got)
	}
	got = s.ClaimNext("host-1", []string{"acme/hot"})
	if got == nil || got.JobID != 1 {
		t.Fatalf("fallback claim = %+v want job 1", got)
	}
}

func TestAssignmentStore_WaitMintedWakesOnPut(t *testing.T) {
	s := NewAssignmentStore()
	done := make(chan struct{})
	go func() {
		s.WaitMinted(context.Background(), time.Second)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	s.Put(&Assignment{JobID: 99, Status: AssignmentMinted, EncodedJITConfig: "jit"})
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitMinted did not wake after Put minted job")
	}
}
