package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApprovalLeaseRecoveryAfterRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	a, err := s.FindOrCreateApproval(ApprovalRow{Approval: Approval{ID: "apv_fixture", Actor: "caller", ArgsRedacted: "{}"}, RequestHash: "hash"}, now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Approval just before the pending deadline gets a fresh five minutes.
	approvedAt := now.Add(30*time.Minute - time.Nanosecond)
	a, err = s.Decide(a.ID, "approved", "admin", "reviewed", approvedAt, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claimedAt := approvedAt.Add(5*time.Minute - time.Nanosecond)
	if err := s.ClaimApprovedForExecution(a.ID, "hash", "caller", claimedAt, "lease", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkConsumed(a.ID, "wrong lease", claimedAt); !errors.Is(err, ErrApprovalState) {
		t.Fatalf("wrong lease: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	deadline := claimedAt.Add(time.Minute)
	before, err := s.ApprovalByID(a.ID, deadline.Add(-time.Nanosecond))
	if err != nil || before.Status != "executing" {
		t.Fatalf("before deadline: %+v %v", before, err)
	}
	rows, pending, err := s.ListApprovals("consumed", 1, deadline)
	if err != nil || pending != 0 || len(rows) != 1 || rows[0].ConsumedAt != activityTimestamp(deadline) || !strings.Contains(rows[0].DecisionNote, "lease expired") {
		t.Fatalf("lease recovery: %+v %d %v", rows, pending, err)
	}
	if err := s.MarkConsumed(a.ID, "lease", deadline.Add(time.Minute)); err != nil {
		t.Fatalf("late finalization of same lease: %v", err)
	}
	if err := s.ClaimApprovedForExecution(a.ID, "hash", "caller", deadline, "another lease", time.Minute); !errors.Is(err, ErrApprovalState) {
		t.Fatalf("reused recovered lease: %v", err)
	}
}
