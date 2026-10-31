package store

import (
	"context"
	"time"
)

// queueTodayMatches - сколько завершённых матчей сайта игрок сыграл сегодня (день по Москве). Считаются все матчи,
// не только из очереди, и любые форматы: игрок, который уже выходил в эфир сегодня, пропускает вперёд остальных.
func queueTodayMatches(ctx context.Context, db queryer, userIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx, `
		SELECT u.id, COUNT(DISTINCT t.id)
		FROM users u
		JOIN participants p ON p.user_id = u.id OR p.members @> jsonb_build_array(jsonb_build_object('userId', u.id))
		JOIN tournaments t ON t.id = p.tournament_id
		WHERE u.id = ANY($1) AND t.status = 'finished' AND COALESCE(t.starts_at, t.created_at) >= $2
		GROUP BY u.id`, userIDs, mskDay(time.Now()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// maxToday - у записи столько матчей за сегодня, сколько у самого игравшего из её игроков.
func maxToday(today map[string]int, users []string) int {
	best := 0
	for _, u := range users {
		if today[u] > best {
			best = today[u]
		}
	}
	return best
}
