// Package api — REST API для веб-интерфейса.
package api

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sobachka/internal/config"
	"sobachka/internal/core"
	"sobachka/internal/ml"
	"sobachka/internal/sources"
	"sobachka/internal/store"
)

type API struct {
	cfg config.Config
	st  *store.Store
	svc *core.Service
	ml  *ml.Client
}

func New(cfg config.Config, st *store.Store, svc *core.Service, mlc *ml.Client) *API {
	return &API{cfg: cfg, st: st, svc: svc, ml: mlc}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/auth", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, current(r)) })

	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	mux.HandleFunc("POST /api/auth/password", a.password)
	mux.HandleFunc("GET /api/users", a.users)
	mux.HandleFunc("POST /api/users", a.saveUser)
	mux.HandleFunc("PUT /api/users/{id}", a.saveUser)
	mux.HandleFunc("GET /api/me/notifications", a.preferences)
	mux.HandleFunc("PUT /api/me/notifications", a.savePreferences)
	mux.HandleFunc("POST /api/me/telegram/link", a.linkTelegram)
	mux.HandleFunc("DELETE /api/me/telegram/link", a.unlinkTelegram)
	mux.HandleFunc("POST /api/me/telegram/test", a.telegramTest)
	mux.HandleFunc("GET /api/telegram/bot", a.botInfo)
	mux.HandleFunc("PUT /api/telegram/bot", a.configureBot)
	mux.HandleFunc("GET /api/stats", a.stats)

	mux.HandleFunc("GET /api/orders", a.listOrders)
	mux.HandleFunc("GET /api/orders/{id}", a.getOrder)
	mux.HandleFunc("POST /api/orders/{id}/feedback", a.orderFeedback)
	mux.HandleFunc("POST /api/orders/{id}/generate", a.orderGenerate)
	mux.HandleFunc("POST /api/orders/{id}/notify", a.orderNotify)
	mux.HandleFunc("POST /api/orders/{id}/rescore", a.orderRescore)

	mux.HandleFunc("GET /api/profiles", a.listProfiles)
	mux.HandleFunc("POST /api/profiles", a.saveProfile)
	mux.HandleFunc("PUT /api/profiles/{id}", a.saveProfile)
	mux.HandleFunc("DELETE /api/profiles/{id}", a.deleteProfile)
	mux.HandleFunc("POST /api/profiles/test", a.testProfile)

	mux.HandleFunc("GET /api/sources", a.listSources)
	mux.HandleFunc("GET /api/sources/kinds", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, sources.Kinds) })
	mux.HandleFunc("GET /api/sources/presets", a.sourcePresets)
	mux.HandleFunc("POST /api/sources", a.saveSource)
	mux.HandleFunc("PUT /api/sources/{id}", a.saveSource)
	mux.HandleFunc("DELETE /api/sources/{id}", a.deleteSource)
	mux.HandleFunc("POST /api/sources/{id}/run", a.runSource)

	mux.HandleFunc("GET /api/settings", a.getSettings)
	mux.HandleFunc("PUT /api/settings", a.putSettings)
	mux.HandleFunc("POST /api/telegram/test", a.telegramTest)

	mux.HandleFunc("GET /api/ml/status", a.mlStatus)
	mux.HandleFunc("POST /api/ml/train", a.mlTrain)

	return a.auth(mux)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	log.Printf("api: %v", err)
	writeErr(w, http.StatusInternalServerError, errors.New("внутренняя ошибка сервера"))
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// ---------- stats ----------

func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	st, err := a.st.Stats(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, st)
}

// ---------- orders ----------

func (a *API) listOrders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.OrderFilter{
		Status: q.Get("status"),
		Query:  strings.TrimSpace(q.Get("q")),
	}
	f.ProfileID, _ = strconv.ParseInt(q.Get("profile_id"), 10, 64)
	f.MinScore, _ = strconv.ParseFloat(q.Get("min_score"), 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	if v := q.Get("feedback"); v != "" {
		n, _ := strconv.Atoi(v)
		f.Feedback = &n
	}
	items, total, err := a.st.ListOrders(r.Context(), f)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"items": items, "total": total})
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	o, err := a.st.GetOrder(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, o)
}

func (a *API) orderFeedback(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var body struct {
		Value int `json:"value"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := a.svc.SetFeedback(r.Context(), id, body.Value); err != nil {
		fail(w, err)
		return
	}
	a.getOrder(w, r)
}

func (a *API) orderGenerate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if _, err := a.svc.Generate(r.Context(), id, -1); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	a.getOrder(w, r)
}

func (a *API) orderNotify(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := a.svc.NotifyUser(r.Context(), id, current(r).ID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	a.getOrder(w, r)
}

func (a *API) orderRescore(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	o, err := a.st.GetOrder(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	profiles, err := a.enabledProfiles(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ev, err := a.svc.Evaluate(r.Context(), o, profiles)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	status := "filtered"
	var pid *int64
	var score *float64
	var matched []string
	if ev.Best != nil {
		status, pid, score, matched = "scored", &ev.Profile.ID, &ev.Best.Score, ev.Best.Keywords
		if ev.Matched {
			status = "matched"
		}
		if o.NotifiedAt != nil {
			status = "notified"
		}
	}
	if err := a.st.SetOrderScore(r.Context(), id, pid, score, matched, status); err != nil {
		fail(w, err)
		return
	}
	a.getOrder(w, r)
}

// ---------- profiles ----------

func (a *API) enabledProfiles(ctx context.Context) ([]store.Profile, error) {
	all, err := a.st.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Profile
	for _, p := range all {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out, nil
}

func (a *API) listProfiles(w http.ResponseWriter, r *http.Request) {
	ps, err := a.st.ListProfiles(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, ps)
}

func cleanList(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s != "" && !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	return out
}

func (a *API) saveProfile(w http.ResponseWriter, r *http.Request) {
	var p store.Profile
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("укажите название профиля"))
		return
	}
	if p.Threshold <= 0 || p.Threshold > 1 {
		p.Threshold = 0.6
	}
	p.Keywords = cleanList(p.Keywords)
	p.MinusWords = cleanList(p.MinusWords)
	var err error
	if r.Method == http.MethodPut {
		if p.ID, err = pathID(r); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		p, err = a.st.UpdateProfile(r.Context(), p)
	} else {
		p, err = a.st.CreateProfile(r.Context(), p)
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, p)
}

func (a *API) deleteProfile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := a.st.DeleteProfile(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// testProfile — «песочница»: оценить произвольный текст заказа и при желании показать отклик по шаблону.
func (a *API) testProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string         `json:"title"`
		Description string         `json:"description"`
		Budget      int            `json:"budget"`
		Profile     *store.Profile `json:"profile"`
		Generate    bool           `json:"generate"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	o := store.Order{Title: body.Title, Description: body.Description}
	if body.Budget > 0 {
		o.BudgetValue = &body.Budget
		o.Budget = strconv.Itoa(body.Budget) + " ₽"
	}
	var profiles []store.Profile
	if body.Profile != nil {
		p := *body.Profile
		p.Keywords, p.MinusWords = cleanList(p.Keywords), cleanList(p.MinusWords)
		if p.Threshold <= 0 {
			p.Threshold = 0.6
		}
		profiles = []store.Profile{p}
	} else {
		var err error
		if profiles, err = a.enabledProfiles(r.Context()); err != nil {
			fail(w, err)
			return
		}
	}
	ev, err := a.svc.Evaluate(r.Context(), o, profiles)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	res := map[string]any{"evaluation": ev}
	if body.Generate && ev.Profile != nil {
		o.Matched = ev.Best.Keywords
		res["pitch"] = core.BuildPitch(o, *ev.Profile, 0)
	}
	writeJSON(w, res)
}

// ---------- sources ----------

func (a *API) listSources(w http.ResponseWriter, r *http.Request) {
	xs, err := a.st.ListSources(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, xs)
}

func (a *API) sourcePresets(w http.ResponseWriter, r *http.Request) {
	ps, err := sources.LoadPresets(a.cfg.PresetsFile)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("готовые площадки: %w", err))
		return
	}
	writeJSON(w, ps)
}

func (a *API) saveSource(w http.ResponseWriter, r *http.Request) {
	var x store.Source
	if err := decode(r, &x); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if _, err := sources.New(x.Kind, nil, ""); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	x.Pattern, x.Cookie = strings.TrimSpace(x.Pattern), strings.TrimSpace(x.Cookie)
	if x.Kind == "html" {
		if x.Pattern == "" {
			writeErr(w, http.StatusBadRequest, errors.New("для HTML-источника укажите шаблон ссылок на заказы"))
			return
		}
		if _, err := regexp.Compile(x.Pattern); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("шаблон ссылок: %w", err))
			return
		}
	}
	if err := sources.ValidateConfig(x.Kind, x.Config); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if u, err := url.Parse(strings.TrimSpace(x.URL)); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeErr(w, http.StatusBadRequest, errors.New("некорректный URL"))
		return
	}
	x.URL = strings.TrimSpace(x.URL)
	if strings.TrimSpace(x.Name) == "" {
		x.Name = x.URL
	}
	if x.IntervalSec < 60 {
		x.IntervalSec = 60
	}
	var err error
	if r.Method == http.MethodPut {
		if x.ID, err = pathID(r); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		x, err = a.st.UpdateSource(r.Context(), x)
	} else {
		x, err = a.st.CreateSource(r.Context(), x)
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, x)
}

func (a *API) deleteSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := a.st.DeleteSource(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) runSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	x, err := a.st.GetSource(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	// Запрос завершится раньше, чем опрос, поэтому отвязываемся от его контекста.
	started := a.svc.RunSource(context.WithoutCancel(r.Context()), x)
	writeJSON(w, map[string]bool{"started": started})
}

// ---------- settings / telegram / ml ----------

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"settings": a.svc.Settings(r.Context()),
		"bot":      a.svc.HasBot(),
	})
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	s := a.svc.Settings(r.Context())
	if err := decode(r, &s); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if s.NotifyMaxAgeMin < 0 || s.NotifyMaxAgeMin > 525600 || s.RetrainEvery < 0 || s.RetrainEvery > 100000 {
		writeErr(w, http.StatusBadRequest, errors.New("некорректный возраст заказа или интервал обучения"))
		return
	}
	if err := a.st.SaveSettings(r.Context(), s); err != nil {
		fail(w, err)
		return
	}
	a.getSettings(w, r)
}

func (a *API) telegramTest(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.SendTest(r.Context(), current(r).ID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *API) mlStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	h, err := a.ml.Health(ctx)
	res := map[string]any{"health": h, "last_train": a.svc.LastTrain(r.Context())}
	if err != nil {
		res["error"] = err.Error()
	}
	writeJSON(w, res)
}

func (a *API) mlTrain(w http.ResponseWriter, r *http.Request) {
	res, err := a.svc.Train(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, res)
}
