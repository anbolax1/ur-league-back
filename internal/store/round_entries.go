package store

import "context"

// Своё выполненное задание приносит очки из каталога (задание - 2, протокол - 1), выполненное
// задание противника - ContractCrossPoints.
const ContractCrossPoints = 1

// KnockPoints - очки за нок рейдера: они входят в ручные очки раунда, а сами ноки идут отдельным счётчиком.
const KnockPoints = 3

// PointsBreakdown — разложение очков участника по источникам (единый источник формулы для
// пересчёта total_points и для статистики профиля).
type PointsBreakdown struct {
	Base      int // ручные очки раунда вместе с ноками (round_entries.points)
	Contracts int // задания и протоколы: очки своих выполненных + 1 × чужие выполненные
	Legendary int // легендарные контракты: SUM(points) выполненных участником
}

// Total — итоговые очки участника в турнире (определяют победителя раунда).
func (b PointsBreakdown) Total() int { return b.Base + b.Contracts + b.Legendary }

// participantBreakdown считает разложение очков участника (без записи в БД):
//
//	base      = SUM(round_entries.points)                              -- ручные очки с ноками
//	contracts = SUM(очки своего выполненного задания + 1 за выполненное задание противника)
//	legendary = SUM(points выполненных легендарных контрактов)
func (s *Store) participantBreakdown(ctx context.Context, participantID string) (PointsBreakdown, error) {
	var b PointsBreakdown
	if err := s.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(points), 0) FROM round_entries WHERE participant_id = $1`,
		participantID).Scan(&b.Base); err != nil {
		return b, err
	}
	// Задания: своё выполненное - очки из каталога, выполненное задание противника - 1.
	// Приведение $2::int обязательно: без него Postgres считает параметр текстом и SUM падает.
	if err := s.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN rbt.participant_id = $1 THEN ct.points ELSE $2::int END), 0)
		FROM round_bonus_tasks rbt
		JOIN catalog_tasks ct ON ct.id = rbt.task_id
		WHERE rbt.completed_by = $1`, participantID, ContractCrossPoints).Scan(&b.Contracts); err != nil {
		return b, err
	}
	// Легендарные контракты, выполненные этим участником.
	if err := s.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(lc.points), 0)
		FROM legendary_contract_completions lcc
		JOIN legendary_contracts lc ON lc.id = lcc.legendary_contract_id
		WHERE lcc.participant_id = $1`, participantID).Scan(&b.Legendary); err != nil {
		return b, err
	}
	return b, nil
}

// RecomputeParticipantPoints пересчитывает и сохраняет total_points участника
// (= participantBreakdown.Total). ErrNotFound — если участника нет.
func (s *Store) RecomputeParticipantPoints(ctx context.Context, participantID string) (int, error) {
	b, err := s.participantBreakdown(ctx, participantID)
	if err != nil {
		return 0, err
	}
	total := b.Total()
	ct, err := s.Pool.Exec(ctx, `UPDATE participants SET total_points = $2 WHERE id = $1`, participantID, total)
	if err != nil {
		return 0, err
	}
	if ct.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	return total, nil
}
