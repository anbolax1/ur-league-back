package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/ur-league/backend/internal/models"
)

const taskCols = `id, text, points, value_type, kind, source, author, title, name, category, tag, map_codes, active`

// taskMaps - карты задания из параметра $n без несуществующих и повторов, в порядке карт на сайте.
func taskMaps(n int) string {
	return fmt.Sprintf(`ARRAY(SELECT code FROM maps WHERE code = ANY($%d::text[]) ORDER BY sort_order)`, n)
}

func scanTask(row pgx.Row) (models.CatalogTask, error) {
	var t models.CatalogTask
	err := row.Scan(&t.ID, &t.Text, &t.Points, &t.ValueType, &t.Kind, &t.Source, &t.Author, &t.Title,
		&t.Name, &t.Category, &t.Tag, &t.MapCodes, &t.Active)
	return t, err
}

// ListCatalogTasks - весь каталог заданий и протоколов: сначала действующие, затем по разделам.
func (s *Store) ListCatalogTasks(ctx context.Context) ([]models.CatalogTask, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+taskCols+` FROM catalog_tasks
		ORDER BY active DESC, category DESC, kind, map_codes, sort_order, text`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.CatalogTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CreateCatalogTask(ctx context.Context, t models.CatalogTask) (models.CatalogTask, error) {
	q := `
		INSERT INTO catalog_tasks (text, points, value_type, kind, source, author, title, name, category, tag, map_codes, active, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, ` + taskMaps(11) + `, $12, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM catalog_tasks))
		RETURNING ` + taskCols
	return scanTask(s.Pool.QueryRow(ctx, q, t.Text, t.Points, t.ValueType, t.Kind, t.Source, t.Author, t.Title,
		t.Name, t.Category, t.Tag, t.MapCodes, t.Active))
}

func (s *Store) UpdateCatalogTask(ctx context.Context, id string, t models.CatalogTask) (models.CatalogTask, error) {
	q := `
		UPDATE catalog_tasks
		SET text = $2, points = $3, value_type = $4, kind = $5, source = $6, author = $7, title = $8,
		    name = $9, category = $10, tag = $11, map_codes = ` + taskMaps(12) + `, active = $13
		WHERE id = $1
		RETURNING ` + taskCols
	return scanTask(s.Pool.QueryRow(ctx, q, id, t.Text, t.Points, t.ValueType, t.Kind, t.Source, t.Author, t.Title,
		t.Name, t.Category, t.Tag, t.MapCodes, t.Active))
}

// DeleteCatalogTask удаляет задание. Если оно уже выдавалось в матчах, удалить нельзя (иначе
// пропадёт история) - тогда задание выключается и больше не раздаётся.
func (s *Store) DeleteCatalogTask(ctx context.Context, id string) error {
	var used bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM round_bonus_tasks WHERE task_id = $1)`, id).Scan(&used); err != nil {
		return err
	}
	if used {
		_, err := s.Pool.Exec(ctx, `UPDATE catalog_tasks SET active = false WHERE id = $1`, id)
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM catalog_tasks WHERE id = $1`, id)
	return err
}
