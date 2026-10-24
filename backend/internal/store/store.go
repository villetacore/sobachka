package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

type Store struct {
	db *pgxpool.Pool
}

type Profile struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Skills      string   `json:"skills"`
	Keywords    []string `json:"keywords"`
	MinusWords  []string `json:"minus_words"`
	MinBudget   int      `json:"min_budget"`
	Threshold   float64  `json:"threshold"`
	PitchNotes  string   `json:"pitch_notes"`
	// Шаблон отклика. Варианты разделяются строкой «---». Подстановки: {title}, {matched}, {direction}, {experience}, {budget}.
	PitchTemplate string    `json:"pitch_template"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

type Source struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	URL         string     `json:"url"`
	Enabled     bool       `json:"enabled"`
	IntervalSec int        `json:"interval_sec"`
	LastRunAt   *time.Time `json:"last_run_at"`
	LastError   string     `json:"last_error"`
	// Для типа html: регулярное выражение для ссылок на заказы. Cookie — для страниц, требующих входа.
	Pattern   string    `json:"pattern"`
	Cookie    string    `json:"cookie"`
	CreatedAt time.Time `json:"created_at"`
	// Настройки разбора для типов json и html (см. sources.Config); пустой объект — по умолчанию.
	Config json.RawMessage `json:"config"`
}

type Order struct {
	ID          int64      `json:"id"`
	SourceID    int64      `json:"source_id"`
	SourceName  string     `json:"source_name"`
	ExternalID  string     `json:"external_id"`
	URL         string     `json:"url"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Category    string     `json:"category"`
	Budget      string     `json:"budget"`
	BudgetValue *int       `json:"budget_value"`
	PublishedAt *time.Time `json:"published_at"`
	CreatedAt   time.Time  `json:"created_at"`
	ProfileID   *int64     `json:"profile_id"`
	ProfileName string     `json:"profile_name"`
	Score       *float64   `json:"score"`
	Status      string     `json:"status"`
	Error       string     `json:"error"`
	Pitch       string     `json:"pitch"`
	Feedback    int        `json:"feedback"`
	Matched     []string   `json:"matched"`
	TgChatID    *int64     `json:"-"`
	TgMessageID *int64     `json:"-"`
	NotifiedAt  *time.Time `json:"notified_at"`
}

// RawOrder — заказ в том виде, в каком его отдал парсер источника.
type RawOrder struct {
	ExternalID  string
	URL         string
	Title       string
	Description string
	Category    string
	Budget      string
	BudgetValue *int
	PublishedAt *time.Time
}

type Settings struct {
	NotifyEnabled   bool `json:"notify_enabled"`
	AutoPitch       bool `json:"auto_pitch"`
	NotifyMaxAgeMin int  `json:"notify_max_age_min"`
	RetrainEvery    int  `json:"retrain_every"`
}

type TrainingExample struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	ProfileID   int64    `json:"profile_id"`
	ProfileName string   `json:"profile_name"`
	ProfileDesc string   `json:"profile_description"`
	Skills      string   `json:"profile_skills"`
	Keywords    []string `json:"profile_keywords"`
	Label       int      `json:"label"`
}

func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	// Postgres в docker-compose может подниматься дольше бэкенда.
	for i := 0; ; i++ {
		if err = pool.Ping(ctx); err == nil {
			break
		}
		if i >= 30 {
			return nil, fmt.Errorf("postgres недоступен: %w", err)
		}
		time.Sleep(2 * time.Second)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("миграция: %w", err)
	}
	return &Store{db: pool}, nil
}

func (s *Store) Close() { s.db.Close() }

// ---------- profiles ----------

const profileCols = `id, name, description, skills, keywords, minus_words, min_budget, threshold, pitch_notes, pitch_template, enabled, created_at`

func scanProfile(row pgx.Row) (Profile, error) {
	var p Profile
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Skills, &p.Keywords, &p.MinusWords,
		&p.MinBudget, &p.Threshold, &p.PitchNotes, &p.PitchTemplate, &p.Enabled, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) ListProfiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.db.Query(ctx, `SELECT `+profileCols+` FROM profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProfile(ctx context.Context, id int64) (Profile, error) {
	return scanProfile(s.db.QueryRow(ctx, `SELECT `+profileCols+` FROM profiles WHERE id=$1`, id))
}

func (s *Store) CreateProfile(ctx context.Context, p Profile) (Profile, error) {
	return scanProfile(s.db.QueryRow(ctx, `
		INSERT INTO profiles (name, description, skills, keywords, minus_words, min_budget, threshold, pitch_notes, pitch_template, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+profileCols,
		p.Name, p.Description, p.Skills, nonNil(p.Keywords), nonNil(p.MinusWords), p.MinBudget, p.Threshold, p.PitchNotes, p.PitchTemplate, p.Enabled))
}

func (s *Store) UpdateProfile(ctx context.Context, p Profile) (Profile, error) {
	return scanProfile(s.db.QueryRow(ctx, `
		UPDATE profiles SET name=$2, description=$3, skills=$4, keywords=$5, minus_words=$6, min_budget=$7,
			threshold=$8, pitch_notes=$9, pitch_template=$10, enabled=$11
		WHERE id=$1 RETURNING `+profileCols,
		p.ID, p.Name, p.Description, p.Skills, nonNil(p.Keywords), nonNil(p.MinusWords), p.MinBudget, p.Threshold, p.PitchNotes, p.PitchTemplate, p.Enabled))
}

func (s *Store) DeleteProfile(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM profiles WHERE id=$1`, id)
	return err
}

// ---------- sources ----------

const sourceCols = `id, kind, name, url, enabled, interval_sec, last_run_at, last_error, pattern, cookie, created_at, config`

func scanSource(row pgx.Row) (Source, error) {
	var x Source
	err := row.Scan(&x.ID, &x.Kind, &x.Name, &x.URL, &x.Enabled, &x.IntervalSec, &x.LastRunAt, &x.LastError, &x.Pattern, &x.Cookie, &x.CreatedAt, &x.Config)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

func (s *Store) ListSources(ctx context.Context) ([]Source, error) {
	rows, err := s.db.Query(ctx, `SELECT `+sourceCols+` FROM sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		x, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) GetSource(ctx context.Context, id int64) (Source, error) {
	return scanSource(s.db.QueryRow(ctx, `SELECT `+sourceCols+` FROM sources WHERE id=$1`, id))
}

func (s *Store) CreateSource(ctx context.Context, x Source) (Source, error) {
	return scanSource(s.db.QueryRow(ctx, `
		INSERT INTO sources (kind, name, url, enabled, interval_sec, pattern, cookie, config) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+sourceCols,
		x.Kind, x.Name, x.URL, x.Enabled, x.IntervalSec, x.Pattern, x.Cookie, configOrEmpty(x.Config)))
}

func (s *Store) UpdateSource(ctx context.Context, x Source) (Source, error) {
	return scanSource(s.db.QueryRow(ctx, `
		UPDATE sources SET kind=$2, name=$3, url=$4, enabled=$5, interval_sec=$6, pattern=$7, cookie=$8, config=$9 WHERE id=$1 RETURNING `+sourceCols,
		x.ID, x.Kind, x.Name, x.URL, x.Enabled, x.IntervalSec, x.Pattern, x.Cookie, configOrEmpty(x.Config)))
}

func configOrEmpty(c json.RawMessage) string {
	if len(c) == 0 || string(c) == "null" {
		return "{}"
	}
	return string(c)
}

func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sources WHERE id=$1`, id)
	return err
}

func (s *Store) MarkSourceRun(ctx context.Context, id int64, errText string) error {
	_, err := s.db.Exec(ctx, `UPDATE sources SET last_run_at=now(), last_error=$2 WHERE id=$1`, id, errText)
	return err
}

// ---------- orders ----------

func (s *Store) SourceHasOrders(ctx context.Context, sourceID int64) (bool, error) {
	var has bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM orders WHERE source_id=$1)`, sourceID).Scan(&has)
	return has, err
}

const orderCols = `o.id, o.source_id, s.name, o.external_id, o.url, o.title, o.description, o.category, o.budget,
	o.budget_value, o.published_at, o.created_at, o.profile_id, COALESCE(p.name, ''), o.score, o.status, o.error,
	o.pitch, o.feedback, o.matched, o.tg_chat_id, o.tg_message_id, o.notified_at`

const orderFrom = ` FROM orders o JOIN sources s ON s.id = o.source_id LEFT JOIN profiles p ON p.id = o.profile_id`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	var fb int16
	err := row.Scan(&o.ID, &o.SourceID, &o.SourceName, &o.ExternalID, &o.URL, &o.Title, &o.Description, &o.Category,
		&o.Budget, &o.BudgetValue, &o.PublishedAt, &o.CreatedAt, &o.ProfileID, &o.ProfileName, &o.Score, &o.Status,
		&o.Error, &o.Pitch, &fb, &o.Matched, &o.TgChatID, &o.TgMessageID, &o.NotifiedAt)
	o.Feedback = int(fb)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// InsertOrder сохраняет заказ, если его ещё нет. inserted=false — дубль.
func (s *Store) InsertOrder(ctx context.Context, sourceID int64, r RawOrder) (id int64, inserted bool, err error) {
	err = s.db.QueryRow(ctx, `
		INSERT INTO orders (source_id, external_id, url, title, description, category, budget, budget_value, published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (source_id, external_id) DO NOTHING RETURNING id`,
		sourceID, r.ExternalID, r.URL, r.Title, r.Description, r.Category, r.Budget, r.BudgetValue, r.PublishedAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func (s *Store) GetOrder(ctx context.Context, id int64) (Order, error) {
	return scanOrder(s.db.QueryRow(ctx, `SELECT `+orderCols+orderFrom+` WHERE o.id=$1`, id))
}

type OrderFilter struct {
	Status    string
	ProfileID int64
	MinScore  float64
	Query     string
	Feedback  *int
	Limit     int
	Offset    int
}

func (s *Store) ListOrders(ctx context.Context, f OrderFilter) ([]Order, int, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	if f.Status != "" {
		add("o.status = ?", f.Status)
	}
	if f.ProfileID > 0 {
		add("o.profile_id = ?", f.ProfileID)
	}
	if f.MinScore > 0 {
		add("o.score >= ?", f.MinScore)
	}
	if f.Query != "" {
		add("(o.title ILIKE ? OR o.description ILIKE ?)", "%"+f.Query+"%")
	}
	if f.Feedback != nil {
		add("o.feedback = ?", *f.Feedback)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*)`+orderFrom+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args = append(args, f.Limit, f.Offset)
	q := `SELECT ` + orderCols + orderFrom + cond +
		fmt.Sprintf(` ORDER BY o.created_at DESC, o.id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}

func (s *Store) SetOrderScore(ctx context.Context, id int64, profileID *int64, score *float64, matched []string, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE orders SET profile_id=$2, score=$3, matched=$4, status=$5, error='' WHERE id=$1`,
		id, profileID, score, nonNil(matched), status)
	return err
}

// SetOrderDetails дополняет заказ данными с его страницы (бюджет — только если его ещё нет).
func (s *Store) SetOrderDetails(ctx context.Context, id int64, description, budget string, budgetValue *int) error {
	_, err := s.db.Exec(ctx, `UPDATE orders SET description=$2,
		budget=CASE WHEN budget='' THEN $3 ELSE budget END,
		budget_value=COALESCE(budget_value, $4) WHERE id=$1`, id, description, budget, budgetValue)
	return err
}

func (s *Store) SetOrderError(ctx context.Context, id int64, msg string) error {
	_, err := s.db.Exec(ctx, `UPDATE orders SET status='error', error=$2 WHERE id=$1`, id, msg)
	return err
}

func (s *Store) SetOrderPitch(ctx context.Context, id int64, pitch string) error {
	_, err := s.db.Exec(ctx, `UPDATE orders SET pitch=$2 WHERE id=$1`, id, pitch)
	return err
}

func (s *Store) SetOrderNotified(ctx context.Context, id, chatID, messageID int64) error {
	_, err := s.db.Exec(ctx, `UPDATE orders SET status='notified', tg_chat_id=$2, tg_message_id=$3, notified_at=now() WHERE id=$1`,
		id, chatID, messageID)
	return err
}

func (s *Store) SetOrderFeedback(ctx context.Context, id int64, value int) error {
	_, err := s.db.Exec(ctx, `UPDATE orders SET feedback=$2::smallint, feedback_at=CASE WHEN $2::smallint=0 THEN NULL ELSE now() END WHERE id=$1`, id, value)
	return err
}

// OrdersToRetry — заказы, которые не успели обработать или упали с ошибкой.
func (s *Store) OrdersToRetry(ctx context.Context, limit int) ([]Order, error) {
	rows, err := s.db.Query(ctx, `SELECT `+orderCols+orderFrom+`
		WHERE o.status IN ('new', 'error') AND o.created_at > now() - interval '2 days'
		ORDER BY o.id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) TrainingExamples(ctx context.Context) ([]TrainingExample, error) {
	rows, err := s.db.Query(ctx, `
		SELECT o.title, o.description, o.category, p.id, p.name, p.description, p.skills, p.keywords, o.feedback
		FROM orders o JOIN profiles p ON p.id = o.profile_id
		WHERE o.feedback <> 0 ORDER BY o.feedback_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrainingExample{}
	for rows.Next() {
		var e TrainingExample
		var fb int16
		if err := rows.Scan(&e.Title, &e.Description, &e.Category, &e.ProfileID, &e.ProfileName, &e.ProfileDesc,
			&e.Skills, &e.Keywords, &fb); err != nil {
			return nil, err
		}
		if fb > 0 {
			e.Label = 1
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecentOrderTexts — тексты последних заказов: на них ML-сервис размечает примеры для самообучения.
func (s *Store) RecentOrderTexts(ctx context.Context, limit int) ([]Order, error) {
	rows, err := s.db.Query(ctx, `SELECT title, left(description, 3000), category FROM orders ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.Title, &o.Description, &o.Category); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) CountFeedbackSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM orders WHERE feedback <> 0 AND feedback_at > $1`, since).Scan(&n)
	return n, err
}

type Stats struct {
	Total    int `json:"total"`
	Today    int `json:"today"`
	Matched  int `json:"matched"`
	Notified int `json:"notified"`
	Positive int `json:"positive"`
	Negative int `json:"negative"`
	Errors   int `json:"errors"`
	Unscored int `json:"unscored"`
	Filtered int `json:"filtered"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.db.QueryRow(ctx, `SELECT
		count(*),
		count(*) FILTER (WHERE created_at > now() - interval '24 hours'),
		count(*) FILTER (WHERE status IN ('matched', 'notified')),
		count(*) FILTER (WHERE status = 'notified'),
		count(*) FILTER (WHERE feedback > 0),
		count(*) FILTER (WHERE feedback < 0),
		count(*) FILTER (WHERE status = 'error'),
		count(*) FILTER (WHERE status = 'new'),
		count(*) FILTER (WHERE status = 'filtered')
		FROM orders`).Scan(&st.Total, &st.Today, &st.Matched, &st.Notified, &st.Positive, &st.Negative, &st.Errors, &st.Unscored, &st.Filtered)
	return st, err
}

// ---------- kv / settings ----------

func (s *Store) GetKV(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRow(ctx, `SELECT value FROM kv WHERE key=$1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetKV(ctx context.Context, key, value string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO kv (key, value) VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, key, value)
	return err
}

// GetSettings возвращает сохранённые настройки поверх значений по умолчанию.
func (s *Store) GetSettings(ctx context.Context, def Settings) (Settings, error) {
	raw, err := s.GetKV(ctx, "settings")
	if err != nil || raw == "" {
		return def, err
	}
	out := def
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return def, nil
	}
	return out, nil
}

func (s *Store) SaveSettings(ctx context.Context, st Settings) error {
	b, _ := json.Marshal(st)
	return s.SetKV(ctx, "settings", string(b))
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
