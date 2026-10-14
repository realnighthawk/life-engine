package engine

import "context"

// historyTables maps an auditable entity type to its table; add an entry with each new entity.
var historyTables = map[string]string{"goal_measurement": "goal_measurements", "tracker_proposal": "tracker_proposals", "reminder": "reminders", "schedule": "schedules", "monitor": "monitors", "settings": "settings", "goal": "goals", "step": "steps", "commitment": "commitments", "plan_item": "plan_items", "plan_proposal": "plan_proposals", "routine": "routines", "journal_entry": "journal_entries", "note": "notes", "capture": "captures", "attachment": "attachments"}

func history(ctx context.Context, u *unit, in HistoryInput) (Page[Change], error) {
	result := Page[Change]{Items: []Change{}}
	key, e := id(in.ID)
	if e != nil {
		return result, e
	}
	table, ok := historyTables[in.EntityType]
	if !ok {
		return result, invalid("unsupported entity_type")
	}
	// History is only available for an existing record, deleted or not.
	var found bool
	if e = u.tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM life."+table+" WHERE id=$1)", key).Scan(&found); e != nil {
		return result, e
	}
	if !found {
		// Hard-deleted records (steps, commitments) keep their audit trail.
		if e = u.tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM life.changes WHERE entity_type=$1 AND entity_id=$2)", in.EntityType, key).Scan(&found); e != nil || !found {
			if e != nil {
				return result, e
			}
			return result, notFound()
		}
	}
	limit, offset, _, e := paging(in.ListInput)
	if e != nil {
		return result, e
	}
	if e = u.tx.QueryRow(ctx, `SELECT count(*) FROM life.changes WHERE entity_type=$1 AND entity_id=$2`, in.EntityType, key).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, `SELECT id,actor_id,operation,entity_type,entity_id,before_data,after_data,occurred_at FROM life.changes
		WHERE entity_type=$1 AND entity_id=$2 ORDER BY id DESC LIMIT $3 OFFSET $4`, in.EntityType, key, limit, offset)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		var c Change
		if e = rows.Scan(&c.ID, &c.Actor, &c.Operation, &c.EntityType, &c.EntityID, &c.Before, &c.After, &c.OccurredAt); e != nil {
			return result, e
		}
		c.OccurredAt = c.OccurredAt.UTC()
		result.Items = append(result.Items, c)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}
