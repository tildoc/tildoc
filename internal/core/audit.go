package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tildoc/tildoc/internal/db"
)

// AuditEvent is one security-relevant change.
type AuditEvent struct {
	ID         string         `json:"id"`
	At         time.Time      `json:"at"`
	Actor      string         `json:"actor"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	SpaceID    *string        `json:"space_id"`
	Details    map[string]any `json:"details"`
}

// audit records a change, in the transaction that makes it.
func (s *Service) audit(ctx context.Context, q db.Querier, p *Principal, action, targetType, targetID, spaceID string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	actor := "system"
	if p != nil && p.Username != "" {
		actor = p.Username
	}
	_, err = q.Exec(ctx, `INSERT INTO audit_events(actor_id, actor, action, target_type, target_id, space_id, details)
		VALUES($1::uuid,$2,$3,$4,$5,$6::uuid,$7)`, p.userRef(), actor, action, targetType, targetID, nonEmpty(spaceID), b)
	return err
}

// ListAuditEvents returns the newest events, before the given id ("" = from the newest).
func (s *Service) ListAuditEvents(ctx context.Context, p *Principal, before string, limit int) ([]AuditEvent, error) {
	if err := requireInst(p, InstAudit); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT id::text, at, actor, action, target_type, target_id, space_id::text, details FROM audit_events
		WHERE ($1 = '' OR id < $1::uuid) ORDER BY id DESC LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.TargetType, &e.TargetID, &e.SpaceID, &e.Details); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
