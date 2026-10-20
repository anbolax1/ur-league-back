package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/ur-league/backend/internal/models"
)

// Сыгранные карты матча (t - его турнир) - карты его заходов; у матча без отмеченных карт - список
// карт матча.

// matchMapsLabel - сыгранные карты матча строкой: сколько заходов на каждой, чаще сыгранная первой.
const matchMapsLabel = `COALESCE(
		(SELECT string_agg(x.name || ' ' || x.n, ' · ' ORDER BY x.n DESC, x.name) FROM (
			SELECT m.name, COUNT(*) AS n FROM round_entries re
			JOIN rounds r ON r.id = re.round_id JOIN maps m ON m.code = re.map_code
			WHERE r.tournament_id = t.id GROUP BY m.name) x),
		(SELECT string_agg(m, ' · ') FROM jsonb_array_elements_text(t.maps) m WHERE m <> ''), '')`

// matchMapsJoin даёт по строке на каждую сыгранную карту матча (mm.map): матч на двух картах идёт
// в статистику обеих.
const matchMapsJoin = `LEFT JOIN LATERAL (
		SELECT m.name FROM round_entries re JOIN rounds r ON r.id = re.round_id JOIN maps m ON m.code = re.map_code
		WHERE r.tournament_id = t.id
		UNION
		SELECT m FROM jsonb_array_elements_text(t.maps) m
		WHERE m <> '' AND NOT EXISTS (
			SELECT 1 FROM round_entries re2 JOIN rounds r2 ON r2.id = re2.round_id
			WHERE r2.tournament_id = t.id AND re2.map_code IS NOT NULL)
	) mm(map) ON true`

// В старых сезонах названия карт набраны капсом и без «ё»: одна карта - одна строка статистики,
// название берётся обычное, не капсом.
const (
	mapStatKey  = `replace(upper(COALESCE(mm.map, '')), 'Ё', 'Е')`
	mapStatName = `COALESCE((array_agg(mm.map ORDER BY mm.map = upper(mm.map), mm.map))[1], '')`
)

// noMapName - строка статистики для матчей, у которых карта не записана.
const noMapName = "Без карты"

// ================== Общая агрегация статистики из ленты матчей ==================

// computeMmrStats считает сводку по хронологической (ASC) ленте матчей: первый матч, пик MMR,
// W/L (взвешенно по жетону ×2), винрейт, лучшие вин/луз-стрики и текущую серию, текущий MMR
// (по последней точке). Место (Place) проставляет вызывающий.
func computeMmrStats(points []models.MmrPoint) models.MmrStats {
	st := models.MmrStats{CurrentMmr: StartMmr, PeakMmr: StartMmr}
	if len(points) == 0 {
		return st
	}
	st.FirstMatch = points[0].Date
	peak := StartMmr
	curKind, curLen := "", 0
	for _, p := range points {
		if p.Correction {
			if p.Mmr > peak {
				peak = p.Mmr
			}
			continue
		}
		w := p.Games
		if w < 1 {
			w = 1
		}
		if p.Win {
			st.Wins += w
		} else {
			st.Losses += w
		}
		if p.Mmr > peak {
			peak = p.Mmr
		}
		kind := "loss"
		if p.Win {
			kind = "win"
		}
		if curKind == kind {
			curLen++
		} else {
			curKind, curLen = kind, 1
		}
		if kind == "win" && curLen > st.BestWinStreak {
			st.BestWinStreak = curLen
		}
		if kind == "loss" && curLen > st.BestLossStreak {
			st.BestLossStreak = curLen
		}
	}
	st.PeakMmr = peak
	st.Games = st.Wins + st.Losses
	if st.Games > 0 {
		st.Winrate = int(math.Round(float64(st.Wins) * 100 / float64(st.Games)))
	}
	st.CurrentStreakKind, st.CurrentStreakLen = curKind, curLen
	st.CurrentMmr = points[len(points)-1].Mmr
	return st
}

// ================== 1×1 (игроки) ==================

// Player1x1Timeline — динамика MMR игрока в 1×1 (ASC по времени): соперник, карты, дельта, исход.
func (s *Store) Player1x1Timeline(ctx context.Context, userID string) ([]models.MmrPoint, error) {
	const q = `
		SELECT COALESCE(h.tournament_id, ''), COALESCE(t.title, 'Сверка рейтинга'), h.created_at, h.delta, h.mmr_after,
		       COALESCE(t.rating_multiplier, 1), COALESCE(t.games, 1), h.season_key, h.tournament_id IS NULL,
		       COALESCE(opp.name, '') AS opp_name,
		       COALESCE(uu.login, '') AS opp_login,
		       ` + matchMapsLabel + ` AS map
		FROM mmr_history h
		LEFT JOIN tournaments t ON t.id = h.tournament_id
		LEFT JOIN LATERAL (
		    SELECT p2.name, p2.user_id FROM participants p2
		    WHERE p2.tournament_id = t.id AND (p2.user_id IS NULL OR p2.user_id <> $1)
		    ORDER BY p2.seed LIMIT 1
		) opp ON true
		LEFT JOIN users uu ON uu.id = opp.user_id
		WHERE h.user_id = $1 AND h.mode = '1x1'
		ORDER BY h.created_at ASC, t.created_at ASC NULLS FIRST`
	return s.scanTimeline(ctx, q, userID)
}

// Player1x1MapsBySeason — разбивка матчей 1×1 по картам в каждом сезоне (взвешенно по ×2); матч на
// нескольких картах засчитан на каждую. Ключ - сезон, пусто - матчи вне сезонов.
func (s *Store) Player1x1MapsBySeason(ctx context.Context, userID string) (map[string][]models.MapStat, error) {
	const q = `
		SELECT COALESCE(h.season_key, '') AS season, ` + mapStatName + ` AS mp,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta > 0), 0)::int AS wins,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta < 0), 0)::int AS losses,
		       COALESCE(SUM(t.games), 0)::int AS games
		FROM mmr_history h
		JOIN tournaments t ON t.id = h.tournament_id
		` + matchMapsJoin + `
		WHERE h.user_id = $1 AND h.mode = '1x1'
		GROUP BY season, ` + mapStatKey + `
		ORDER BY season, games DESC, mp`
	rows, err := s.Pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]models.MapStat{}
	for rows.Next() {
		var season string
		var m models.MapStat
		if err := rows.Scan(&season, &m.Map, &m.Wins, &m.Losses, &m.Games); err != nil {
			return nil, err
		}
		if m.Map == "" {
			m.Map = noMapName
		}
		out[season] = append(out[season], m)
	}
	return out, rows.Err()
}

// Player1x1OpponentsBySeason — head-to-head игрока в 1×1 в каждом сезоне (взвешенно по ×2), частые
// соперники сверху. Ключ - сезон, пусто - матчи вне сезонов.
func (s *Store) Player1x1OpponentsBySeason(ctx context.Context, userID string) (map[string][]models.OpponentStat, error) {
	const q = `
		SELECT COALESCE(h.season_key, '') AS season,
		       COALESCE(uu.login, '') AS opp_login, COALESCE(opp.name, '') AS opp_name,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta > 0), 0)::int AS wins,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta < 0), 0)::int AS losses,
		       COALESCE(SUM(t.games), 0)::int AS games
		FROM mmr_history h
		JOIN tournaments t ON t.id = h.tournament_id
		LEFT JOIN LATERAL (
		    SELECT p2.name, p2.user_id FROM participants p2
		    WHERE p2.tournament_id = t.id AND (p2.user_id IS NULL OR p2.user_id <> $1)
		    ORDER BY p2.seed LIMIT 1
		) opp ON true
		LEFT JOIN users uu ON uu.id = opp.user_id
		WHERE h.user_id = $1 AND h.mode = '1x1'
		GROUP BY season, opp_login, opp_name
		ORDER BY season, games DESC, opp_name`
	rows, err := s.Pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]models.OpponentStat{}
	for rows.Next() {
		var season string
		var o models.OpponentStat
		if err := rows.Scan(&season, &o.Login, &o.Name, &o.Wins, &o.Losses, &o.Games); err != nil {
			return nil, err
		}
		out[season] = append(out[season], o)
	}
	return out, rows.Err()
}

// Player1x1KnocksBySeason - киллы игрока в матчах 1×1 по сезонам. Ключ - сезон, пусто - матчи вне сезонов.
func (s *Store) Player1x1KnocksBySeason(ctx context.Context, userID string) (map[string]models.KnockStats, error) {
	const q = `
		SELECT COALESCE(t.season_id, '') AS season, t.id, COALESCE(opp.name, ''),
		       (SELECT COALESCE(SUM(re.knocks), 0) FROM round_entries re WHERE re.participant_id = p.id)::int
		FROM participants p
		JOIN tournaments t ON t.id = p.tournament_id
		LEFT JOIN LATERAL (
		    SELECT p2.name FROM participants p2
		    WHERE p2.tournament_id = t.id AND p2.id <> p.id
		    ORDER BY p2.seed LIMIT 1
		) opp ON true
		WHERE p.user_id = $1 AND t.mode = '1x1' AND t.status = 'finished'
		  AND EXISTS (SELECT 1 FROM rounds r JOIN round_entries re ON re.round_id = r.id
		              WHERE r.tournament_id = t.id AND re.knocks > 0)
		ORDER BY COALESCE(t.starts_at, t.created_at)`
	rows, err := s.Pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]models.KnockStats{}
	for rows.Next() {
		var season, matchID, opponent string
		var knocks int
		if err := rows.Scan(&season, &matchID, &opponent, &knocks); err != nil {
			return nil, err
		}
		k := out[season]
		k.Matches++
		k.Knocks += knocks
		// Матчи идут по порядку: при равенстве рекордом остаётся первый.
		if knocks > k.Best {
			k.Best, k.BestMatch, k.BestOpponent = knocks, matchID, opponent
		}
		out[season] = k
	}
	return out, rows.Err()
}

// Player1x1Place - место игрока в таблице 1×1 текущего сезона, как на странице рейтинга: по MMR,
// при равном - по победам; 0 - в этом сезоне ещё не играл.
func (s *Store) Player1x1Place(ctx context.Context, userID string) (int, error) {
	rows, err := s.Leaderboard(ctx, "1x1", s.activeSeasonRule(ctx).Key)
	if err != nil {
		return 0, err
	}
	for i, r := range rows {
		if r.UserID == userID {
			return i + 1, nil
		}
	}
	return 0, nil
}

// ================== составы 2×2 и 3×3 (команды) ==================

// teamMembersJSON - состав команды tm массивом JSON в порядке ключа: двое у 2×2, трое у 3×3.
const teamMembersJSON = `(SELECT COALESCE(json_agg(json_build_object('userId', u.id, 'login', u.login,
		'displayName', u.display_name, 'avatarUrl', u.avatar_url)
		ORDER BY array_position(ARRAY[tm.member_a, tm.member_b, tm.member_c], u.id)), '[]'::json)
	FROM users u WHERE u.id IN (tm.member_a, tm.member_b, tm.member_c))`

// opponentNamesSQL - логины игроков команды-соперника tmo через « & »: так она подписана в ленте и разбивках.
const opponentNamesSQL = `COALESCE((SELECT string_agg(u.login, ' & '
		ORDER BY array_position(ARRAY[tmo.member_a, tmo.member_b, tmo.member_c], u.id))
	FROM users u WHERE u.id IN (tmo.member_a, tmo.member_b, tmo.member_c)), '')`

// TeamsForUser — список команд игрока обоих форматов (краткие карточки с MMR и W/L), лучшие сверху.
func (s *Store) TeamsForUser(ctx context.Context, userID string) ([]models.TeamSummary, error) {
	q := `
		SELECT tm.team_key, tm.name, tm.mode, tm.mmr, ` + teamMembersJSON + ` AS members,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta > 0), 0)::int AS wins,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta < 0), 0)::int AS losses
		FROM team_mmr tm
		LEFT JOIN team_mmr_history h ON h.team_key = tm.team_key
		LEFT JOIN tournaments t ON t.id = h.tournament_id AND t.status = 'finished'
		WHERE $1 IN (tm.member_a, tm.member_b, tm.member_c)
		GROUP BY tm.team_key
		ORDER BY tm.mmr DESC`
	rows, err := s.Pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.TeamSummary{}
	for rows.Next() {
		var ts models.TeamSummary
		var members []byte
		if err := rows.Scan(&ts.TeamKey, &ts.Name, &ts.Mode, &ts.Mmr, &members, &ts.Wins, &ts.Losses); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(members, &ts.Members); err != nil {
			return nil, err
		}
		ts.Games = ts.Wins + ts.Losses
		out = append(out, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if p, err := s.TeamPlace(ctx, out[i].TeamKey); err == nil {
			out[i].Place = p
		}
	}
	return out, nil
}

// TeamMembers возвращает состав команды, её текущий MMR, название и формат; ok=false, если команды нет.
func (s *Store) TeamMembers(ctx context.Context, teamKey string) (members []models.TeamMember, mmr int, name, mode string, ok bool, err error) {
	var raw []byte
	if s.Pool.QueryRow(ctx, `SELECT tm.mmr, tm.name, tm.mode, `+teamMembersJSON+` FROM team_mmr tm WHERE tm.team_key = $1`,
		teamKey).Scan(&mmr, &name, &mode, &raw) != nil {
		return nil, 0, "", "", false, nil
	}
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, 0, "", "", false, err
	}
	return members, mmr, name, mode, true, nil
}

// TeamPlace - место команды в таблице её формата (2×2 или 3×3) текущего сезона среди сыгравших в нём;
// 0 - в этом сезоне не играла. Остальные команды держат стартовый MMR и в таблицу сезона не входят.
func (s *Store) TeamPlace(ctx context.Context, teamKey string) (int, error) {
	var place int
	err := s.Pool.QueryRow(ctx, `
		WITH played AS (
			SELECT tm.team_key, tm.mmr FROM team_mmr tm
			WHERE tm.mode = (SELECT mode FROM team_mmr WHERE team_key = $1)
			  AND EXISTS (SELECT 1 FROM team_mmr_history h
			              WHERE h.team_key = tm.team_key AND COALESCE(h.season_key, '') = $2)
		)
		SELECT 1 + (SELECT COUNT(*) FROM played p WHERE p.mmr > me.mmr) FROM played me WHERE me.team_key = $1`,
		teamKey, s.activeSeasonRule(ctx).Key).Scan(&place)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return place, err
}

// TeamTimeline — динамика MMR команды (ASC по времени): соперник (команда), карты, дельта, исход.
func (s *Store) TeamTimeline(ctx context.Context, teamKey string) ([]models.MmrPoint, error) {
	const q = `
		SELECT h.tournament_id, t.title, h.created_at, h.delta, h.mmr_after, t.rating_multiplier, t.games, h.season_key,
		       COALESCE(opp.team_key, '') AS opp_key,
		       ` + opponentNamesSQL + ` AS opp_name,
		       ` + matchMapsLabel + ` AS map
		FROM team_mmr_history h
		JOIN tournaments t ON t.id = h.tournament_id
		LEFT JOIN LATERAL (
		    SELECT h2.team_key FROM team_mmr_history h2
		    WHERE h2.tournament_id = t.id AND h2.team_key <> $1 LIMIT 1
		) opp ON true
		LEFT JOIN team_mmr tmo ON tmo.team_key = opp.team_key
		WHERE h.team_key = $1
		ORDER BY h.created_at ASC, t.created_at ASC`
	rows, err := s.Pool.Query(ctx, q, teamKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.MmrPoint{}
	for rows.Next() {
		var p models.MmrPoint
		var opp string
		if err := rows.Scan(&p.TournamentID, &p.Title, &p.Date, &p.Delta, &p.Mmr, &p.Mult, &p.Games, &p.Season,
			&p.OpponentKey, &opp, &p.Map); err != nil {
			return nil, err
		}
		p.Opponent = teamNameOrDash(opp)
		p.Win = p.Delta > 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// TeamMaps — разбивка матчей команды по картам (взвешенно по ×2); матч на нескольких картах
// засчитан на каждую.
func (s *Store) TeamMaps(ctx context.Context, teamKey string) ([]models.MapStat, error) {
	const q = `
		SELECT ` + mapStatName + ` AS mp,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta > 0), 0)::int AS wins,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta < 0), 0)::int AS losses,
		       COALESCE(SUM(t.games), 0)::int AS games
		FROM team_mmr_history h
		JOIN tournaments t ON t.id = h.tournament_id
		` + matchMapsJoin + `
		WHERE h.team_key = $1
		GROUP BY ` + mapStatKey + `
		ORDER BY games DESC, mp`
	return s.scanMapStats(ctx, q, teamKey)
}

// TeamOpponents — head-to-head команды против других команд (взвешенно по ×2).
func (s *Store) TeamOpponents(ctx context.Context, teamKey string) ([]models.OpponentStat, error) {
	const q = `
		SELECT COALESCE(opp.team_key, '') AS opp_key,
		       ` + opponentNamesSQL + ` AS opp_name,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta > 0), 0)::int AS wins,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta < 0), 0)::int AS losses,
		       COALESCE(SUM(t.games), 0)::int AS games
		FROM team_mmr_history h
		JOIN tournaments t ON t.id = h.tournament_id
		LEFT JOIN LATERAL (
		    SELECT h2.team_key FROM team_mmr_history h2
		    WHERE h2.tournament_id = t.id AND h2.team_key <> $1 LIMIT 1
		) opp ON true
		LEFT JOIN team_mmr tmo ON tmo.team_key = opp.team_key
		WHERE h.team_key = $1
		GROUP BY opp.team_key, tmo.team_key
		ORDER BY games DESC`
	rows, err := s.Pool.Query(ctx, q, teamKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.OpponentStat{}
	for rows.Next() {
		var o models.OpponentStat
		var opp string
		if err := rows.Scan(&o.TeamKey, &opp, &o.Wins, &o.Losses, &o.Games); err != nil {
			return nil, err
		}
		o.Name = teamNameOrDash(opp)
		out = append(out, o)
	}
	return out, rows.Err()
}

// TeamMapsBySeason - разбивка матчей команды по картам в каждом сезоне (взвешенно по ×2); матч на
// нескольких картах засчитан на каждую. Ключ - сезон, пусто - матчи вне сезонов.
func (s *Store) TeamMapsBySeason(ctx context.Context, teamKey string) (map[string][]models.MapStat, error) {
	const q = `
		SELECT COALESCE(h.season_key, '') AS season, ` + mapStatName + ` AS mp,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta > 0), 0)::int AS wins,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta < 0), 0)::int AS losses,
		       COALESCE(SUM(t.games), 0)::int AS games
		FROM team_mmr_history h
		JOIN tournaments t ON t.id = h.tournament_id
		` + matchMapsJoin + `
		WHERE h.team_key = $1
		GROUP BY season, ` + mapStatKey + `
		ORDER BY season, games DESC, mp`
	rows, err := s.Pool.Query(ctx, q, teamKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]models.MapStat{}
	for rows.Next() {
		var season string
		var m models.MapStat
		if err := rows.Scan(&season, &m.Map, &m.Wins, &m.Losses, &m.Games); err != nil {
			return nil, err
		}
		if m.Map == "" {
			m.Map = noMapName
		}
		out[season] = append(out[season], m)
	}
	return out, rows.Err()
}

// TeamOpponentsBySeason - соперники команды в каждом сезоне (взвешенно по ×2), частые сверху.
// Ключ - сезон, пусто - матчи вне сезонов.
func (s *Store) TeamOpponentsBySeason(ctx context.Context, teamKey string) (map[string][]models.OpponentStat, error) {
	const q = `
		SELECT COALESCE(h.season_key, '') AS season, COALESCE(opp.team_key, '') AS opp_key,
		       ` + opponentNamesSQL + ` AS opp_name,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta > 0), 0)::int AS wins,
		       COALESCE(SUM(t.games) FILTER (WHERE h.delta < 0), 0)::int AS losses,
		       COALESCE(SUM(t.games), 0)::int AS games
		FROM team_mmr_history h
		JOIN tournaments t ON t.id = h.tournament_id
		LEFT JOIN LATERAL (
		    SELECT h2.team_key FROM team_mmr_history h2
		    WHERE h2.tournament_id = t.id AND h2.team_key <> $1 LIMIT 1
		) opp ON true
		LEFT JOIN team_mmr tmo ON tmo.team_key = opp.team_key
		WHERE h.team_key = $1
		GROUP BY season, opp.team_key, tmo.team_key
		ORDER BY season, games DESC`
	rows, err := s.Pool.Query(ctx, q, teamKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]models.OpponentStat{}
	for rows.Next() {
		var season, opp string
		var o models.OpponentStat
		if err := rows.Scan(&season, &o.TeamKey, &opp, &o.Wins, &o.Losses, &o.Games); err != nil {
			return nil, err
		}
		o.Name = teamNameOrDash(opp)
		out[season] = append(out[season], o)
	}
	return out, rows.Err()
}

// ================== общие помощники ==================

func (s *Store) scanTimeline(ctx context.Context, q, arg string) ([]models.MmrPoint, error) {
	rows, err := s.Pool.Query(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.MmrPoint{}
	for rows.Next() {
		var p models.MmrPoint
		if err := rows.Scan(&p.TournamentID, &p.Title, &p.Date, &p.Delta, &p.Mmr, &p.Mult, &p.Games, &p.Season,
			&p.Correction, &p.Opponent, &p.OpponentKey, &p.Map); err != nil {
			return nil, err
		}
		p.Win = p.Delta > 0
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) scanMapStats(ctx context.Context, q, arg string) ([]models.MapStat, error) {
	rows, err := s.Pool.Query(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.MapStat{}
	for rows.Next() {
		var m models.MapStat
		if err := rows.Scan(&m.Map, &m.Wins, &m.Losses, &m.Games); err != nil {
			return nil, err
		}
		if m.Map == "" {
			m.Map = noMapName
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// teamNameOrDash - имя команды-соперника; прочерк, если состав не нашёлся.
func teamNameOrDash(name string) string {
	if name == "" {
		return "—"
	}
	return name
}

// PlayerStatsBundle собирает расширенную статистику 1×1 игрока: ленту, сводку, разбивку по картам и
// соперникам и киллы отдельно по сезонам - в каждом сезоне рейтинг начинается заново.
func (s *Store) PlayerStatsBundle(ctx context.Context, userID string) (models.MmrStats, []models.MmrPoint, map[string]models.SeasonAnalytics, error) {
	timeline, err := s.Player1x1Timeline(ctx, userID)
	if err != nil {
		return models.MmrStats{}, nil, nil, err
	}
	maps, err := s.Player1x1MapsBySeason(ctx, userID)
	if err != nil {
		return models.MmrStats{}, nil, nil, err
	}
	opps, err := s.Player1x1OpponentsBySeason(ctx, userID)
	if err != nil {
		return models.MmrStats{}, nil, nil, err
	}
	knocks, err := s.Player1x1KnocksBySeason(ctx, userID)
	if err != nil {
		return models.MmrStats{}, nil, nil, err
	}
	analytics := map[string]models.SeasonAnalytics{}
	for season := range maps {
		analytics[season] = seasonAnalytics(maps[season], opps[season], knocks[season])
	}
	for season := range opps {
		analytics[season] = seasonAnalytics(maps[season], opps[season], knocks[season])
	}
	// Ничья не меняет MMR и не попадает в карты и соперников, а киллы в ней есть.
	for season := range knocks {
		analytics[season] = seasonAnalytics(maps[season], opps[season], knocks[season])
	}
	stats := computeMmrStats(timeline)
	// Лента - за все сезоны, а текущий MMR - в текущем сезоне (в начале сезона у всех стартовый).
	if cur, err := s.GetUserMmr(ctx, userID, "1x1"); err == nil {
		stats.CurrentMmr = cur
	}
	if place, err := s.Player1x1Place(ctx, userID); err == nil {
		stats.Place = place
	}
	return stats, timeline, analytics, nil
}

// seasonAnalytics - разбивка одного сезона; пустой список уходит массивом, а не null.
func seasonAnalytics(maps []models.MapStat, opps []models.OpponentStat, knocks models.KnockStats) models.SeasonAnalytics {
	if maps == nil {
		maps = []models.MapStat{}
	}
	if opps == nil {
		opps = []models.OpponentStat{}
	}
	return models.SeasonAnalytics{Maps: maps, Opponents: opps, Knocks: knocks}
}

// TeamProfile собирает полную статистику команды (2×2 или 3×3). ok=false — команды нет.
func (s *Store) TeamProfile(ctx context.Context, teamKey string) (models.TeamProfile, bool, error) {
	members, mmr, name, mode, ok, err := s.TeamMembers(ctx, teamKey)
	if err != nil || !ok {
		return models.TeamProfile{}, ok, err
	}
	timeline, err := s.TeamTimeline(ctx, teamKey)
	if err != nil {
		return models.TeamProfile{}, false, err
	}
	maps, err := s.TeamMaps(ctx, teamKey)
	if err != nil {
		return models.TeamProfile{}, false, err
	}
	opps, err := s.TeamOpponents(ctx, teamKey)
	if err != nil {
		return models.TeamProfile{}, false, err
	}
	mapsBySeason, err := s.TeamMapsBySeason(ctx, teamKey)
	if err != nil {
		return models.TeamProfile{}, false, err
	}
	oppsBySeason, err := s.TeamOpponentsBySeason(ctx, teamKey)
	if err != nil {
		return models.TeamProfile{}, false, err
	}
	analytics := map[string]models.SeasonAnalytics{}
	for season := range mapsBySeason {
		analytics[season] = seasonAnalytics(mapsBySeason[season], oppsBySeason[season], models.KnockStats{})
	}
	for season := range oppsBySeason {
		analytics[season] = seasonAnalytics(mapsBySeason[season], oppsBySeason[season], models.KnockStats{})
	}
	stats := computeMmrStats(timeline)
	// Лента - за все сезоны, а MMR и место - в текущем сезоне (не игравшая в нём команда вне таблицы).
	stats.CurrentMmr = mmr
	if place, err := s.TeamPlace(ctx, teamKey); err == nil {
		stats.Place = place
	}
	return models.TeamProfile{
		TeamKey:   teamKey,
		Name:      name,
		Mode:      mode,
		Members:   members,
		Stats:     stats,
		Timeline:  timeline,
		Maps:      maps,
		Opponents: opps,
		Analytics: analytics,
	}, true, nil
}
