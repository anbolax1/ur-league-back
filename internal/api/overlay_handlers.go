package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
	"github.com/ur-league/backend/internal/models"
)

// stateEnvelope оборачивает сырое состояние оверлея в конверт для WS/клиента.
// presetsRev — счётчик правок пресетов: по его смене страницы /overlay/<slug>
// перечитывают свою раскладку (у них она не из состояния, а из пресета).
func (s *Server) stateEnvelope(stateJSON []byte) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type":       "state",
		"state":      json.RawMessage(stateJSON),
		"presetsRev": s.presetsRev.Load(),
	})
}

// overlayStateBytes — что показывать оверлею (источник правды — БД):
//   - нет идущего матча → "{}" (плашка «никто не в эфире»);
//   - матч идёт → его состояние, сохранённое при последнем действии в пульте, а без него - собранное из БД.
func (s *Server) overlayStateBytes(ctx context.Context) []byte {
	data, _ := s.Store.GetLiveState(ctx)
	var stored models.LiveState
	_ = json.Unmarshal(data, &stored)

	id, _ := s.Store.CurrentMatchID(ctx)
	if id == "" {
		// Итог завершённого матча остаётся на экране, пока не начнётся следующий.
		if stored.Stage == "finished" {
			return data
		}
		return []byte("{}")
	}
	if stored.Status == "live" && stored.TournamentID != nil && *stored.TournamentID == id && stored.TournamentName != "" {
		return data
	}
	st, err := s.Store.GetMatchState(ctx, id)
	if err != nil {
		return []byte("{}")
	}
	if b, err := json.Marshal(s.matchLiveState(ctx, st, stored)); err == nil {
		return b
	}
	return []byte("{}")
}

// republishOverlay рассылает текущее состояние оверлея; идущий матч сервер пересобирает сам.
func (s *Server) republishOverlay(ctx context.Context) {
	if id, err := s.Store.CurrentMatchID(ctx); err == nil && id != "" {
		if st, err := s.Store.GetMatchState(ctx, id); err == nil {
			s.publishMatchState(ctx, st)
			return
		}
	}
	if env, err := s.stateEnvelope(s.overlayStateBytes(ctx)); err == nil {
		s.Hub.Broadcast(env)
	}
}

func (s *Server) handleGetOverlayState(w http.ResponseWriter, r *http.Request) {
	writeRaw(w, http.StatusOK, s.overlayStateBytes(r.Context()))
}

// handleGetOverlayLayout — раскладка выбранной сцены (её показывает /overlay) с её id в activePresetId.
// В отличие от /overlay/state не зависит от того, есть ли сейчас live-турнир, — редактор с любого
// устройства открывает ту же сцену. Сцена не выбрана → "{}" (фронт подставит дефолт).
func (s *Server) handleGetOverlayLayout(w http.ResponseWriter, r *http.Request) {
	data, _ := s.Store.GetLiveState(r.Context())
	var stored models.LiveState
	if err := json.Unmarshal(data, &stored); err == nil && stored.Layout != nil {
		writeJSON(w, http.StatusOK, stored.Layout)
		return
	}
	writeRaw(w, http.StatusOK, []byte("{}"))
}

// handleOverlayWS — поток живого состояния для OBS-оверлея.
func (s *Server) handleOverlayWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"}, // OBS browser source присылает разные origin
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// CloseRead обрабатывает ping/pong и закрытие; прикладные сообщения от клиента не ждём.
	ctx := c.CloseRead(r.Context())

	client := s.Hub.Register()
	defer s.Hub.Unregister(client)

	// Отправляем актуальное состояние сразу при подключении (с учётом live-турнира из БД).
	if env, err := s.stateEnvelope(s.overlayStateBytes(r.Context())); err == nil {
		_ = c.Write(ctx, websocket.MessageText, env)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-client.Send:
			if !ok {
				return
			}
			if err := c.Write(ctx, websocket.MessageText, msg); err != nil {
				return
			}
		}
	}
}
