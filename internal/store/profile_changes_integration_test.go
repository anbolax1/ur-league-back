package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ur-league/backend/internal/db"
	"github.com/ur-league/backend/internal/models"
	"github.com/ur-league/backend/internal/store"
)

// Заявки на смену ника и фото, удаление игрока и призы сезона против реальной Postgres. Запуск:
//
//	UR_TEST_DB=postgres://ur:ur@localhost:5434/ur_league_it go test ./internal/store -run TestProfileChanges -v
//
// Без переменной - пропускается (в т.ч. в CI).
func TestProfileChangesAndHiddenPlayers(t *testing.T) {
	url := os.Getenv("UR_TEST_DB")
	if url == "" {
		t.Skip("UR_TEST_DB не задан - интеграционный тест пропущен")
	}
	if err := db.Migrate(url); err != nil {
		t.Fatalf("миграции: %v", err)
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatalf("подключение: %v", err)
	}
	defer pool.Close()
	st := store.New(pool)

	uniq := time.Now().UnixNano()
	mkUser := func(tag string) models.User {
		login := fmt.Sprintf("it_%d_%s", uniq, tag)
		u, err := st.CreateUser(ctx, login, login, "x", models.RoleUser)
		if err != nil {
			t.Fatalf("CreateUser(%s): %v", tag, err)
		}
		return u
	}
	a, b, mod := mkUser("a"), mkUser("b"), mkUser("mod")

	// Ник: заявка, повторная заявка заменяет прежнюю, одобрение меняет ник.
	first, _, err := st.SubmitProfileChange(ctx, a.ID, store.ChangeName, "Новый"+fmt.Sprint(uniq))
	if err != nil {
		t.Fatalf("заявка на ник: %v", err)
	}
	second, _, err := st.SubmitProfileChange(ctx, a.ID, store.ChangeName, "Ещё"+fmt.Sprint(uniq))
	if err != nil {
		t.Fatalf("вторая заявка на ник: %v", err)
	}
	if _, err := st.GetProfileChange(ctx, first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("прежняя заявка должна исчезнуть: %v", err)
	}
	if _, _, err := st.SubmitProfileChange(ctx, b.ID, store.ChangeName, a.Login); !errors.Is(err, store.ErrNameTaken) {
		t.Errorf("чужой логин как ник: ждали ErrNameTaken, получили %v", err)
	}
	done, _, err := st.DecideProfileChange(ctx, second.ID, true, "", mod.ID)
	if err != nil || done.Status != "approved" {
		t.Fatalf("одобрение: %+v %v", done, err)
	}
	if u, _ := st.GetUser(ctx, a.ID); u.DisplayName != second.Value {
		t.Errorf("ник после одобрения: %q", u.DisplayName)
	}
	if _, _, err := st.DecideProfileChange(ctx, second.ID, true, "", mod.ID); !errors.Is(err, store.ErrChangeDecided) {
		t.Errorf("повторное решение: %v", err)
	}

	// Фото: отклонение возвращает файл на удаление и не трогает профиль; одобрение ставит адрес.
	pic, _, err := st.SubmitProfileChange(ctx, b.ID, store.ChangeAvatar, "avatars/x1.jpg")
	if err != nil {
		t.Fatalf("заявка на фото: %v", err)
	}
	if pic.Value != "/media/avatars/x1.jpg" {
		t.Errorf("адрес фото в заявке: %q", pic.Value)
	}
	rej, dropped, err := st.DecideProfileChange(ctx, pic.ID, false, "не видно лица", mod.ID)
	if err != nil || rej.Status != "rejected" || rej.Reason != "не видно лица" || dropped != "avatars/x1.jpg" {
		t.Fatalf("отклонение: %+v файл %q %v", rej, dropped, err)
	}
	pic2, _, _ := st.SubmitProfileChange(ctx, b.ID, store.ChangeAvatar, "avatars/x2.jpg")
	if _, _, err := st.DecideProfileChange(ctx, pic2.ID, true, "", mod.ID); err != nil {
		t.Fatalf("одобрение фото: %v", err)
	}
	if u, _ := st.GetUser(ctx, b.ID); u.AvatarURL != "/media/avatars/x2.jpg" {
		t.Errorf("фото после одобрения: %q", u.AvatarURL)
	}
	pic3, _, _ := st.SubmitProfileChange(ctx, b.ID, store.ChangeAvatar, "avatars/x3.jpg")
	if _, dropped, err := st.DecideProfileChange(ctx, pic3.ID, true, "", mod.ID); err != nil || dropped != "avatars/x2.jpg" {
		t.Errorf("прежнее фото на удаление: %q %v", dropped, err)
	}
	pending, err := st.PendingProfileChanges(ctx)
	if err != nil {
		t.Fatalf("очередь: %v", err)
	}
	for _, c := range pending {
		if c.UserID == a.ID || c.UserID == b.ID {
			t.Errorf("рассмотренная заявка осталась в очереди: %+v", c)
		}
	}

	// Удаление игрока: вход закрыт, он в списке скрытых, возврат всё открывает.
	if err := st.SetUserHidden(ctx, b.ID, true); err != nil {
		t.Fatalf("скрыть: %v", err)
	}
	if _, _, _, err := st.GetUserAuthByLogin(ctx, b.Login); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("вход скрытого: %v", err)
	}
	if logins, _ := st.HiddenLogins(ctx); !slices.Contains(logins, b.Login) {
		t.Errorf("логин не в списке скрытых: %v", logins)
	}
	players, err := st.ListMatchPlayers(ctx)
	if err != nil {
		t.Fatalf("игроки для матча: %v", err)
	}
	for _, p := range players {
		if p.ID == b.ID {
			t.Error("скрытый игрок попал в выбор для матча")
		}
	}
	if has, _ := st.TeamHasHiddenMember(ctx, a.ID+"|"+b.ID); !has {
		t.Error("команда со скрытым игроком не определилась")
	}
	if err := st.SetUserHidden(ctx, b.ID, false); err != nil {
		t.Fatalf("вернуть: %v", err)
	}
	if _, _, _, err := st.GetUserAuthByLogin(ctx, b.Login); err != nil {
		t.Errorf("вход после возврата: %v", err)
	}

	// Призы сезона сохраняются вместе с остальными настройками.
	sn, err := st.StartNewSeason(ctx, fmt.Sprintf("it-%d", uniq), 32, 1000)
	if err != nil {
		t.Fatalf("сезон: %v", err)
	}
	prizes := models.SeasonPrizes{Pool: 30000, Places: []int{15000, 10000, 5000}, Note: "Выплата на карту"}
	upd, err := st.UpdateSeason(ctx, sn.ID, sn.Name, sn.StartedAt, nil, 32, 1000, prizes)
	if err != nil {
		t.Fatalf("призы сезона: %v", err)
	}
	if upd.Prizes.Pool != 30000 || !slices.Equal(upd.Prizes.Places, prizes.Places) || upd.Prizes.Note != prizes.Note {
		t.Errorf("призы после сохранения: %+v", upd.Prizes)
	}
}
