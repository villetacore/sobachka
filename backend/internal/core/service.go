// Package core — конвейер: сбор заказов → оценка нейросетью → генерация отклика → уведомление.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"sobachka/internal/config"
	"sobachka/internal/ml"
	"sobachka/internal/sources"
	"sobachka/internal/store"
	"sobachka/internal/telegram"
)

type job struct {
	id     int64
	notify bool
}

type Service struct {
	cfg       config.Config
	st        *store.Store
	ml        *ml.Client
	bot       *telegram.Bot
	botMu     sync.Mutex
	botInfo   BotConfig
	botCtx    context.Context
	botCancel context.CancelFunc
	http      *http.Client
	queue     chan job
	running   sync.Map
	trainMu   sync.Mutex
}

func New(cfg config.Config, st *store.Store, mlc *ml.Client, bot *telegram.Bot) *Service {
	return &Service{
		cfg:   cfg,
		st:    st,
		ml:    mlc,
		bot:   bot,
		http:  &http.Client{Timeout: cfg.FetchTimeout},
		queue: make(chan job, 2000),
	}
}

func (s *Service) DefaultSettings() store.Settings {
	return store.Settings{
		NotifyEnabled:   true,
		AutoPitch:       true,
		NotifyMaxAgeMin: int(s.cfg.NotifyMaxAge / time.Minute),
		RetrainEvery:    s.cfg.RetrainEvery,
	}
}

func (s *Service) Settings(ctx context.Context) store.Settings {
	st, err := s.st.GetSettings(ctx, s.DefaultSettings())
	if err != nil {
		log.Printf("settings: %v", err)
	}
	return st
}

// Run запускает планировщик, обработчик очереди и Telegram-бота.
func (s *Service) Run(ctx context.Context) {
	go s.worker(ctx)
	s.retryPending(ctx)

	tick := time.NewTicker(15 * time.Second)
	retry := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	defer retry.Stop()
	for {
		s.scheduleSources(ctx)
		select {
		case <-ctx.Done():
			return
		case <-retry.C:
			s.retryPending(ctx)
			s.periodicTrain(ctx)
		case <-tick.C:
		}
	}
}

func (s *Service) scheduleSources(ctx context.Context) {
	srcs, err := s.st.ListSources(ctx)
	if err != nil {
		log.Printf("sources: %v", err)
		return
	}
	for _, src := range srcs {
		if !src.Enabled {
			continue
		}
		interval := time.Duration(max(src.IntervalSec, 30)) * time.Second
		if src.LastRunAt != nil && time.Since(*src.LastRunAt) < interval {
			continue
		}
		s.RunSource(ctx, src)
	}
}

// RunSource запускает опрос источника в фоне; false — если он уже выполняется.
func (s *Service) RunSource(ctx context.Context, src store.Source) bool {
	if _, busy := s.running.LoadOrStore(src.ID, struct{}{}); busy {
		return false
	}
	go func() {
		defer s.running.Delete(src.ID)
		s.fetchSource(ctx, src)
	}()
	return true
}

func (s *Service) fetchSource(ctx context.Context, src store.Source) {
	f, err := sources.New(src.Kind, s.http, s.cfg.UserAgent)
	if err != nil {
		_ = s.st.MarkSourceRun(ctx, src.ID, err.Error())
		return
	}
	fctx, cancel := context.WithTimeout(ctx, s.cfg.FetchTimeout)
	items, err := f.Fetch(fctx, src)
	cancel()
	errText := ""
	if err != nil {
		errText = err.Error()
		log.Printf("источник %q: %v", src.Name, err)
	}
	if err := s.st.MarkSourceRun(ctx, src.ID, errText); err != nil {
		log.Printf("источник %q: %v", src.Name, err)
	}
	if len(items) == 0 {
		return
	}

	// Первый прогон источника — это «бэклог», его оцениваем, но в Telegram не шлём.
	// Пока от источника нет ни одного заказа, опрос тоже считается первым: иначе, если первый
	// опрос упал с ошибкой, весь бэклог пришёл бы в Telegram при следующем.
	firstRun := src.LastRunAt == nil
	if !firstRun {
		has, err := s.st.SourceHasOrders(ctx, src.ID)
		firstRun = err == nil && !has
	}
	maxAge := time.Duration(s.Settings(ctx).NotifyMaxAgeMin) * time.Minute
	added := 0
	for _, it := range items {
		if it.Title == "" || it.URL == "" {
			continue
		}
		id, inserted, err := s.st.InsertOrder(ctx, src.ID, it)
		if err != nil {
			log.Printf("сохранение заказа: %v", err)
			continue
		}
		if !inserted {
			continue
		}
		if d, ok := f.(sources.Detailer); ok && it.Description == "" {
			s.loadDetails(ctx, d, src, id, it.URL)
		}
		added++
		fresh := it.PublishedAt == nil || maxAge <= 0 || time.Since(*it.PublishedAt) <= maxAge
		select {
		case s.queue <- job{id: id, notify: !firstRun && fresh}:
		case <-ctx.Done():
			return
		}
	}
	if added > 0 {
		log.Printf("источник %q: новых заказов %d", src.Name, added)
	}
}

// loadDetails догружает описание заказа с его страницы (для html-источников).
func (s *Service) loadDetails(ctx context.Context, d sources.Detailer, src store.Source, id int64, url string) {
	dctx, cancel := context.WithTimeout(ctx, s.cfg.FetchTimeout)
	defer cancel()
	desc, budget, value, err := d.Details(dctx, src, url)
	if err != nil {
		log.Printf("заказ %d: описание со страницы: %v", id, err)
		return
	}
	if err := s.st.SetOrderDetails(ctx, id, desc, budget, value); err != nil {
		log.Printf("заказ %d: %v", id, err)
	}
	// Не нагружаем сайт: небольшая пауза между страницами.
	select {
	case <-ctx.Done():
	case <-time.After(700 * time.Millisecond):
	}
}

func (s *Service) retryPending(ctx context.Context) {
	orders, err := s.st.OrdersToRetry(ctx, 500)
	if err != nil {
		log.Printf("retry: %v", err)
		return
	}
	maxAge := time.Duration(s.Settings(ctx).NotifyMaxAgeMin) * time.Minute
	for _, o := range orders {
		fresh := time.Since(o.CreatedAt) <= maxAge
		select {
		case s.queue <- job{id: o.ID, notify: fresh}:
		default:
			return
		}
	}
}

func (s *Service) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.queue:
			s.waitML(ctx)
			if err := s.process(ctx, j); err != nil && ctx.Err() == nil {
				log.Printf("заказ %d: %v", j.id, err)
				_ = s.st.SetOrderError(ctx, j.id, err.Error())
			}
		}
	}
}

// waitML ждёт, пока ML-сервис станет доступен.
func (s *Service) waitML(ctx context.Context) {
	logged := false
	for ctx.Err() == nil {
		hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		h, err := s.ml.Health(hctx)
		cancel()
		if err == nil && h.Status == "ok" {
			if logged {
				log.Printf("ML-сервис готов")
			}
			return
		}
		if !logged {
			log.Printf("жду ML-сервис… (%v)", err)
			logged = true
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func toMLOrder(o store.Order) ml.Order {
	return ml.Order{Title: o.Title, Description: o.Description, Category: o.Category}
}

func toMLProfile(p store.Profile) ml.Profile {
	return ml.Profile{ID: p.ID, Name: p.Name, Keywords: p.Keywords}
}

// Evaluation — результат оценки заказа относительно всех профилей.
type Evaluation struct {
	Best     *ml.Score        `json:"best"`
	Profile  *store.Profile   `json:"profile"`
	Matched  bool             `json:"matched"`
	Scores   []ml.Score       `json:"scores"`
	Filtered map[int64]string `json:"filtered"`
}

// Evaluate применяет жёсткие фильтры профилей (минус-слова, бюджет) и оценивает заказ нейросетью.
func (s *Service) Evaluate(ctx context.Context, o store.Order, profiles []store.Profile) (Evaluation, error) {
	ev := Evaluation{Filtered: map[int64]string{}}
	text := strings.ToLower(o.Title + "\n" + o.Description)
	var candidates []ml.Profile
	byID := map[int64]store.Profile{}
	for _, p := range profiles {
		if reason := hardFilter(text, o.BudgetValue, p); reason != "" {
			ev.Filtered[p.ID] = reason
			continue
		}
		candidates = append(candidates, toMLProfile(p))
		byID[p.ID] = p
	}
	if len(candidates) == 0 {
		return ev, nil
	}
	scores, err := s.ml.Score(ctx, toMLOrder(o), candidates)
	if err != nil {
		return ev, err
	}
	ev.Scores = scores
	for i := range scores {
		if ev.Best == nil || scores[i].Score > ev.Best.Score {
			ev.Best = &scores[i]
		}
	}
	if ev.Best != nil {
		p := byID[ev.Best.ProfileID]
		ev.Profile = &p
		ev.Matched = ev.Best.Score >= p.Threshold
	}
	return ev, nil
}

func hardFilter(text string, budget *int, p store.Profile) string {
	for _, w := range p.MinusWords {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" && strings.Contains(text, w) {
			return "минус-слово: " + w
		}
	}
	if p.MinBudget > 0 && budget != nil && *budget < p.MinBudget {
		return fmt.Sprintf("бюджет %d < %d", *budget, p.MinBudget)
	}
	return ""
}

func (s *Service) process(ctx context.Context, j job) error {
	o, err := s.st.GetOrder(ctx, j.id)
	if err != nil {
		return err
	}
	all, err := s.st.ListProfiles(ctx)
	if err != nil {
		return err
	}
	var profiles []store.Profile
	for _, p := range all {
		if p.Enabled {
			profiles = append(profiles, p)
		}
	}
	if len(profiles) == 0 {
		return s.st.SetOrderScore(ctx, o.ID, nil, nil, nil, "scored")
	}

	var ev Evaluation
	for attempt := 0; ; attempt++ {
		ev, err = s.Evaluate(ctx, o, profiles)
		if err == nil || attempt >= 2 || ctx.Err() != nil {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		return err
	}
	if ev.Best == nil {
		return s.st.SetOrderScore(ctx, o.ID, nil, nil, nil, "filtered")
	}
	if ev.Best.Score == 0 {
		// Ни одного совпадения: к профилю не привязываем, чтобы оценки таких заказов
		// не учили нейросеть, будто они относятся к первому профилю в списке.
		zero := 0.0
		return s.st.SetOrderScore(ctx, o.ID, nil, &zero, nil, "scored")
	}
	status := "scored"
	if ev.Matched {
		status = "matched"
	}
	score := ev.Best.Score
	if err := s.st.SetOrderScore(ctx, o.ID, &ev.Profile.ID, &score, ev.Best.Keywords, status); err != nil {
		return err
	}
	if ev.Matched && j.notify {
		log.Printf("заказ %d подходит (%.0f%%, %s): %s", o.ID, score*100, ev.Profile.Name, o.Title)
		return s.deliver(ctx, o.ID)
	}
	return nil
}

// deliver собирает отклик (если включено) и отправляет уведомление.
func (s *Service) deliver(ctx context.Context, id int64) error {
	set := s.Settings(ctx)
	if !set.NotifyEnabled || !s.HasBot() {
		return nil
	}
	if set.AutoPitch {
		if _, err := s.Generate(ctx, id, 0); err != nil {
			log.Printf("заказ %d: отклик: %v", id, err)
		}
	}
	return s.Notify(ctx, id)
}

// Generate собирает текст отклика из шаблона профиля и сохраняет его.
// variant < 0 — взять вариант, следующий за текущим (кнопка «Другой вариант»).
func (s *Service) Generate(ctx context.Context, id int64, variant int) (string, error) {
	o, err := s.st.GetOrder(ctx, id)
	if err != nil {
		return "", err
	}
	var p store.Profile
	if o.ProfileID != nil {
		p, err = s.st.GetProfile(ctx, *o.ProfileID)
	}
	if o.ProfileID == nil || errors.Is(err, store.ErrNotFound) {
		ps, err := s.st.ListProfiles(ctx)
		if err != nil || len(ps) == 0 {
			return "", errors.New("нет ни одного профиля")
		}
		p = ps[0]
	} else if err != nil {
		return "", err
	}
	if variant < 0 {
		variant = 0
		for i := 0; i < PitchVariantCount(p); i++ {
			if BuildPitch(o, p, i) == o.Pitch {
				variant = i + 1
				break
			}
		}
	}
	text := BuildPitch(o, p, variant)
	return text, s.st.SetOrderPitch(ctx, id, text)
}

// SetFeedback сохраняет оценку пользователя и при необходимости запускает дообучение.
func (s *Service) SetFeedback(ctx context.Context, id int64, value int) error {
	if value > 1 || value < -1 {
		return errors.New("feedback должен быть -1, 0 или 1")
	}
	if err := s.st.SetOrderFeedback(ctx, id, value); err != nil {
		return err
	}
	s.maybeRetrain(ctx)
	return nil
}

type TrainState struct {
	At     time.Time       `json:"at"`
	Result *ml.TrainResult `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (s *Service) LastTrain(ctx context.Context) TrainState {
	var ts TrainState
	if raw, _ := s.st.GetKV(ctx, "last_train"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &ts)
	}
	return ts
}

func (s *Service) maybeRetrain(ctx context.Context) {
	every := s.Settings(ctx).RetrainEvery
	if every <= 0 {
		return
	}
	n, err := s.st.CountFeedbackSince(ctx, s.LastTrain(ctx).At)
	if err != nil || n < every {
		return
	}
	go func() {
		if _, err := s.Train(context.WithoutCancel(ctx)); err != nil {
			log.Printf("дообучение: %v", err)
		}
	}()
}

// periodicTrain раз в trainPeriod передаёт ML-сервису свежие заказы и профили и дообучает сеть,
// даже если новых 👍/👎 не было. Между этими запусками сервис дообучается сам по мере новых заказов.
const trainPeriod = 6 * time.Hour

func (s *Service) periodicTrain(ctx context.Context) {
	if time.Since(s.LastTrain(ctx).At) < trainPeriod {
		return
	}
	go func() {
		if _, err := s.Train(context.WithoutCancel(ctx)); err != nil {
			log.Printf("плановое дообучение: %v", err)
		}
	}()
}

// Train дообучает классификатор релевантности на оценках пользователя.
func (s *Service) Train(ctx context.Context) (ml.TrainResult, error) {
	if !s.trainMu.TryLock() {
		return ml.TrainResult{}, errors.New("обучение уже идёт")
	}
	defer s.trainMu.Unlock()
	examples, err := s.st.TrainingExamples(ctx)
	if err != nil {
		return ml.TrainResult{}, err
	}
	recent, err := s.st.RecentOrderTexts(ctx, 3000)
	if err != nil {
		return ml.TrainResult{}, err
	}
	unlabeled := make([]ml.Order, 0, len(recent))
	for _, o := range recent {
		unlabeled = append(unlabeled, toMLOrder(o))
	}
	all, err := s.st.ListProfiles(ctx)
	if err != nil {
		return ml.TrainResult{}, err
	}
	profiles := []ml.Profile{}
	for _, p := range all {
		if p.Enabled {
			profiles = append(profiles, toMLProfile(p))
		}
	}
	res, err := s.ml.Train(ctx, examples, unlabeled, profiles)
	ts := TrainState{At: time.Now()}
	if err != nil {
		// Счётчик новых оценок не сбрасываем — попробуем снова, когда их станет больше.
		ts = s.LastTrain(ctx)
		ts.Error = err.Error()
	} else {
		ts.Result = &res
		log.Printf("классификатор дообучен: %d примеров (ваших оценок %d, авторазметки %d), accuracy %.2f",
			res.Samples, res.UserSamples, res.AutoSamples, res.Accuracy)
	}
	b, _ := json.Marshal(ts)
	_ = s.st.SetKV(ctx, "last_train", string(b))
	return res, err
}
