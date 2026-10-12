package engine

import "context"

func history(ctx context.Context, u *unit, in HistoryInput) (Page[Change], error) {
	result := Page[Change]{Items: []Change{}}
	key, e := id(in.ID)
	if e != nil {
		return result, e
	}
	if !oneOf(in.EntityType, "reminder") {
		return result, invalid("unsupported entity_type")
	}
	// History is only available for an existing record, deleted or not.
	if _, e = u.getReminder(ctx, key, false); e != nil {
		return result, e
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
