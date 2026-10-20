package store

import (
	"context"
	"encoding/json"

	"github.com/ur-league/backend/internal/models"
)

// seasonStart - стартовый MMR сезона для таблицы: конкретного сезона либо текущего (для «за всё время»).
func (s *Store) seasonStart(ctx context.Context, seasonID string) int {
	if seasonID == "" {
		return s.activeSeasonRule(ctx).Start
	}
	if sn, err := s.GetSeason(ctx, seasonID); err == nil {
		return sn.StartMmr
	}
	return StartMmr
}

// Leaderboard агрегирует рейтинг 1×1 по игрокам. В рейтинг идут ТОЛЬКО завершённые турниры
// (status='finished'). MMR считается внутри сезона: в таблице сезона - MMR этого сезона, в таблице
// «за всё время» - MMR текущего сезона. wins, losses и games учитывают жетон ×2 (матч = 2);
// ничья - ни победа, ни поражение, но матч.
//
//	seasonID="" — за всё время; иначе только турниры этого сезона.
func (s *Store) Leaderboard(ctx context.Context, mode, seasonID string) ([]models.LeaderboardRow, error) {
	const q = `
		SELECT u.id, u.login, u.display_name, u.avatar_url,
		       (CASE WHEN $1 = '' THEN COALESCE(um.mmr, $2::int)
		             ELSE $2::int + COALESCE((SELECT SUM(h.delta) FROM mmr_history h
		                  WHERE h.user_id = u.id AND h.mode = '1x1' AND h.season_key = $1), 0)
		        END)::int AS mmr,
		       COALESCE(SUM(p.total_points), 0)::int AS points,
		       COALESCE(SUM(CASE WHEN t.winner_participant_id = p.id THEN t.games ELSE 0 END), 0)::int AS wins,
		       COALESCE(SUM(CASE WHEN t.winner_participant_id <> p.id THEN t.games ELSE 0 END), 0)::int AS losses,
		       COALESCE(SUM(t.games), 0)::int AS games
		FROM participants p
		JOIN tournaments t ON t.id = p.tournament_id AND t.mode = '1x1' AND t.status = 'finished'
		   AND ($1 = '' OR t.season_id = $1)
		JOIN users u ON u.id = p.user_id
		LEFT JOIN user_mmr um ON um.user_id = u.id AND um.mode = '1x1'
		WHERE p.kind = 'player'
		GROUP BY u.id, u.login, u.display_name, u.avatar_url, um.mmr
		ORDER BY mmr DESC, wins DESC`

	rows, err := s.Pool.Query(ctx, q, seasonID, s.seasonStart(ctx, seasonID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.LeaderboardRow{}
	for rows.Next() {
		var r models.LeaderboardRow
		if err := rows.Scan(&r.UserID, &r.Login, &r.DisplayName, &r.AvatarURL, &r.Mmr, &r.Points, &r.Wins, &r.Losses, &r.Tournaments); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TeamLeaderboard агрегирует рейтинг составов формата mode (2x2 или 3x3) по КОМАНДАМ: состав =
// команда с одним MMR, ключ - набор userId. MMR - внутри сезона, как у игроков. wins/losses/games
// учитывают жетон ×2 (матч = 2) и считаются по завершённым матчам (сезонный фильтр - по ним же).
// Показываются только команды, сыгравшие хотя бы один завершённый матч (в сезоне, если задан).
func (s *Store) TeamLeaderboard(ctx context.Context, mode, seasonID string) ([]models.TeamLeaderboardRow, error) {
	q := `
		SELECT tm.team_key, tm.name, tm.mode,
		       (CASE WHEN $1 = '' THEN tm.mmr
		             ELSE $2::int + COALESCE((SELECT SUM(h2.delta) FROM team_mmr_history h2
		                  WHERE h2.team_key = tm.team_key AND h2.season_key = $1), 0)
		        END)::int AS mmr,
		       COALESCE(SUM(CASE WHEN h.delta > 0 THEN t.games ELSE 0 END), 0)::int AS wins,
		       COALESCE(SUM(CASE WHEN h.delta < 0 THEN t.games ELSE 0 END), 0)::int AS losses,
		       ` + teamMembersJSON + ` AS members
		FROM team_mmr tm
		JOIN team_mmr_history h ON h.team_key = tm.team_key
		JOIN tournaments t ON t.id = h.tournament_id AND t.status = 'finished'
		   AND ($1 = '' OR t.season_id = $1)
		WHERE tm.mode = $3
		GROUP BY tm.team_key
		ORDER BY mmr DESC, wins DESC`

	rows, err := s.Pool.Query(ctx, q, seasonID, s.seasonStart(ctx, seasonID), mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.TeamLeaderboardRow{}
	for rows.Next() {
		var r models.TeamLeaderboardRow
		var members []byte
		if err := rows.Scan(&r.TeamKey, &r.Name, &r.Mode, &r.Mmr, &r.Wins, &r.Losses, &members); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(members, &r.Members); err != nil {
			return nil, err
		}
		r.Games = r.Wins + r.Losses
		out = append(out, r)
	}
	return out, rows.Err()
}
