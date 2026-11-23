package store

import (
	"database/sql"
	"errors"
	"time"
)

var ErrPendingLimit = errors.New("pending approval limit reached")
var ErrApprovalState = errors.New("approval transition unavailable")

func migrateApprovalsV10(tx DBTX) error {
	_, err := tx.Exec(`
CREATE TABLE approvals (
 id TEXT PRIMARY KEY, status TEXT NOT NULL CHECK(status IN ('pending','approved','executing','consumed','denied','expired')),
 request_hash TEXT NOT NULL, actor TEXT NOT NULL, agent TEXT NOT NULL DEFAULT '', requester_session TEXT NOT NULL DEFAULT '',
 repo_id TEXT NOT NULL DEFAULT '', repo_name TEXT NOT NULL DEFAULT '', protocol TEXT NOT NULL, service_id TEXT NOT NULL,
 action TEXT NOT NULL, read_only INTEGER NOT NULL DEFAULT 0, rule_id TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '',
 args_redacted TEXT NOT NULL CHECK(json_valid(args_redacted)), summary TEXT NOT NULL, created_at TEXT NOT NULL, pending_expires_at TEXT NOT NULL,
 approved_at TEXT NOT NULL DEFAULT '', expires_at TEXT NOT NULL DEFAULT '', decided_by TEXT NOT NULL DEFAULT '',
 decided_at TEXT NOT NULL DEFAULT '', decision_note TEXT NOT NULL DEFAULT '', lease_id TEXT NOT NULL DEFAULT '',
 lease_expires_at TEXT NOT NULL DEFAULT '', consumed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX approvals_status_created ON approvals(status,created_at DESC);
CREATE INDEX approvals_hash_status ON approvals(request_hash,status);
CREATE INDEX approvals_actor_status ON approvals(actor,status);
PRAGMA user_version=10;`)
	return err
}

type Approval struct {
	ID, Status, Actor, Agent, RequesterSession                     string
	RepoID, RepoName, Protocol, ServiceID, Action                  string
	ReadOnly                                                       bool
	RuleID, Reason, ArgsRedacted, Summary                          string
	CreatedAt, PendingExpiresAt, ApprovedAt, ExpiresAt             string
	DecidedBy, DecidedAt, DecisionNote, LeaseExpiresAt, ConsumedAt string
}

type ApprovalRow struct {
	Approval
	RequestHash string
}

const approvalColumns = `id,status,actor,agent,requester_session,repo_id,repo_name,protocol,service_id,action,
read_only,rule_id,reason,args_redacted,summary,created_at,pending_expires_at,approved_at,expires_at,
decided_by,decided_at,decision_note,lease_expires_at,consumed_at,request_hash`

func scanApproval(row interface{ Scan(...any) error }) (ApprovalRow, error) {
	var a ApprovalRow
	err := row.Scan(&a.ID, &a.Status, &a.Actor, &a.Agent, &a.RequesterSession, &a.RepoID, &a.RepoName, &a.Protocol, &a.ServiceID, &a.Action,
		&a.ReadOnly, &a.RuleID, &a.Reason, &a.ArgsRedacted, &a.Summary, &a.CreatedAt, &a.PendingExpiresAt, &a.ApprovedAt, &a.ExpiresAt,
		&a.DecidedBy, &a.DecidedAt, &a.DecisionNote, &a.LeaseExpiresAt, &a.ConsumedAt, &a.RequestHash)
	return a, wrapNotFound(err)
}

// Fixed-width UTC timestamps preserve chronological order in SQLite TEXT comparisons.
func expireApprovals(tx DBTX, now string) error {
	for _, q := range []string{
		"UPDATE approvals SET status='expired' WHERE status='pending' AND pending_expires_at <= ?1",
		"UPDATE approvals SET status='expired' WHERE status='approved' AND expires_at <= ?1",
		`UPDATE approvals SET status='consumed', consumed_at=?1,
decision_note=trim(decision_note || ' [lease expired]') WHERE status='executing' AND lease_expires_at <= ?1`,
	} {
		if _, err := tx.Exec(q, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) FindOrCreateApproval(a ApprovalRow, now time.Time, pendingTTL time.Duration) (ApprovalRow, error) {
	ts := activityTimestamp(now)
	var out ApprovalRow
	err := s.Tx(func(tx DBTX) error {
		if err := expireApprovals(tx, ts); err != nil {
			return err
		}
		var err error
		out, err = scanApproval(tx.QueryRow("SELECT "+approvalColumns+" FROM approvals WHERE request_hash=? AND actor=? AND status IN ('approved','pending')", a.RequestHash, a.Actor))
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		var count int
		if err := tx.QueryRow("SELECT COUNT(*) FROM approvals WHERE actor=? AND agent=? AND status='pending'", a.Actor, a.Agent).Scan(&count); err != nil {
			return err
		}
		if count >= 20 {
			return ErrPendingLimit
		}
		a.Status, a.CreatedAt, a.PendingExpiresAt = "pending", ts, activityTimestamp(now.Add(pendingTTL))
		_, err = tx.Exec(`INSERT INTO approvals(id,status,request_hash,actor,agent,requester_session,repo_id,repo_name,protocol,service_id,
action,read_only,rule_id,reason,args_redacted,summary,created_at,pending_expires_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.ID, a.Status, a.RequestHash, a.Actor, a.Agent, a.RequesterSession, a.RepoID, a.RepoName, a.Protocol, a.ServiceID, a.Action, a.ReadOnly, a.RuleID, a.Reason, a.ArgsRedacted, a.Summary, a.CreatedAt, a.PendingExpiresAt)
		out = a
		return err
	})
	return out, err
}

func (s *Store) ApprovalByID(id string, now time.Time) (ApprovalRow, error) {
	var a ApprovalRow
	err := s.Tx(func(tx DBTX) error {
		if err := expireApprovals(tx, activityTimestamp(now)); err != nil {
			return err
		}
		var err error
		a, err = scanApproval(tx.QueryRow("SELECT "+approvalColumns+" FROM approvals WHERE id=?", id))
		return err
	})
	return a, err
}

func (s *Store) ListApprovals(status string, limit int, now time.Time) ([]Approval, int, error) {
	items := []Approval{}
	var pending int
	err := s.Tx(func(tx DBTX) error {
		if err := expireApprovals(tx, activityTimestamp(now)); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT COUNT(*) FROM approvals WHERE status='pending'").Scan(&pending); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT "+approvalColumns+" FROM approvals WHERE (?1='' OR status=?1) ORDER BY (status='pending') DESC,created_at DESC,id DESC LIMIT ?2", status, limit)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			a, err := scanApproval(rows)
			if err != nil {
				return err
			}
			items = append(items, a.Approval)
		}
		return rows.Err()
	})
	return items, pending, err
}

func (s *Store) Decide(id, decision, decidedBy, note string, now time.Time, execTTL time.Duration) (ApprovalRow, error) {
	if decision != "approved" && decision != "denied" {
		return ApprovalRow{}, ErrApprovalState
	}
	ts := activityTimestamp(now)
	approved, expires := "", ""
	if decision == "approved" {
		approved, expires = ts, activityTimestamp(now.Add(execTTL))
	}
	return scanApproval(s.QueryRow(`UPDATE approvals SET status=?,decided_by=?,decision_note=?,decided_at=?,approved_at=?,expires_at=?
WHERE id=? AND status='pending' AND pending_expires_at>? RETURNING `+approvalColumns, decision, decidedBy, note, ts, approved, expires, id, ts))
}

func (s *Store) RevokeApproved(id, hash, actor string, now time.Time) error {
	_, err := s.Exec(`UPDATE approvals SET status='denied',decision_note='policy_denied',decided_at=?,decided_by='policy'
WHERE id=? AND request_hash=? AND actor=? AND status='approved'`, activityTimestamp(now), id, hash, actor)
	return err
}

func (s *Store) ClaimApprovedForExecution(id, requestHash, actor string, now time.Time, leaseID string, leaseTTL time.Duration) error {
	return oneApprovalChanged(s.Exec(`UPDATE approvals SET status='executing',lease_id=?,lease_expires_at=?
WHERE id=? AND status='approved' AND request_hash=? AND actor=? AND expires_at>?`,
		leaseID, activityTimestamp(now.Add(leaseTTL)), id, requestHash, actor, activityTimestamp(now)))
}

func (s *Store) MarkConsumed(id, leaseID string, now time.Time) error {
	return oneApprovalChanged(s.Exec(`UPDATE approvals SET status='consumed',
consumed_at=CASE WHEN status='executing' THEN ?1 ELSE consumed_at END,
decision_note=CASE WHEN status='executing' AND lease_expires_at<=?1 THEN trim(decision_note || ' [lease expired]') ELSE decision_note END
WHERE id=?2 AND lease_id=?3 AND status IN ('executing','consumed')`, activityTimestamp(now), id, leaseID))
}

func oneApprovalChanged(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrApprovalState
	}
	return err
}
