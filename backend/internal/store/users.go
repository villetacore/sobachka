package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"sobachka/internal/security"
	"time"
)

type User struct {
	ID             int64  `json:"id"`
	Username       string `json:"username"`
	PasswordHash   string `json:"-"`
	Role           string `json:"role"`
	Enabled        bool   `json:"enabled"`
	TelegramChatID *int64 `json:"telegram_chat_id"`
	NotifyEnabled  bool   `json:"notify_enabled"`
	AllProfiles    bool   `json:"all_profiles"`
}

const userCols = `id,username,password_hash,role,enabled,telegram_chat_id,notify_enabled,all_profiles`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.TelegramChatID, &u.NotifyEnabled, &u.AllProfiles)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return u, err
}
func (s *Store) User(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}
func (s *Store) UserByLogin(ctx context.Context, login string) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE username=$1`, login))
}
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) Bootstrap(ctx context.Context, login, password string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(8712301)`); err != nil {
		return err
	}
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return tx.Commit(ctx)
	}
	login = security.Login(login)
	if !security.ValidLogin(login) {
		return errors.New("задайте ADMIN_USERNAME: 3–64 символа a-z, 0-9, _, ., -")
	}
	hash, err := security.Password(password)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO users(username,password_hash,role) VALUES($1,$2,'admin')`, login, hash)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) CreateUser(ctx context.Context, u User) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,$2,$3,$4) RETURNING `+userCols, u.Username, u.PasswordHash, u.Role, u.Enabled))
}
func (s *Store) UpdateUser(ctx context.Context, u User) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(8712301)`); err != nil {
		return err
	}
	old, err := scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1 FOR UPDATE`, u.ID))
	if err != nil {
		return err
	}
	if old.Enabled && old.Role == "admin" && (!u.Enabled || u.Role != "admin") {
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE enabled AND role='admin'`).Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return errors.New("нельзя отключить или понизить последнего администратора")
		}
	}
	if u.PasswordHash == "" {
		u.PasswordHash = old.PasswordHash
	}
	_, err = tx.Exec(ctx, `UPDATE users SET role=$2,enabled=$3,password_hash=$4 WHERE id=$1`, u.ID, u.Role, u.Enabled, u.PasswordHash)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, u.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM telegram_links WHERE user_id=$1`, u.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ChangePassword(ctx context.Context, id int64, oldHash, newHash string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE users SET password_hash=$3 WHERE id=$1 AND password_hash=$2 AND enabled`, id, oldHash, newHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM telegram_links WHERE user_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) NewSession(ctx context.Context, u User, hash string) error {
	// Recheck the password hash under a row lock, so concurrent resets cannot resurrect sessions.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND enabled AND password_hash=$2 FOR UPDATE`, u.ID, u.PasswordHash).Scan(&id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE expires_at<now() OR user_id=$1 AND token_hash IN (SELECT token_hash FROM sessions WHERE user_id=$1 ORDER BY expires_at DESC OFFSET 9)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '12 hours')`, hash, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Session(ctx context.Context, hash string) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE enabled AND id=(SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at>now())`, hash))
}
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hash)
	return err
}
func (s *Store) AllowLogin(ctx context.Context, key string, limit int) (bool, error) {
	_, err := s.db.Exec(ctx, `DELETE FROM login_limits WHERE reset_at<now()`)
	if err != nil {
		return false, err
	}
	var attempts int
	err = s.db.QueryRow(ctx, `INSERT INTO login_limits(key,attempts,reset_at) VALUES($1,1,now()+interval '15 minutes') ON CONFLICT(key) DO UPDATE SET attempts=login_limits.attempts+1 RETURNING attempts`, key).Scan(&attempts)
	return attempts <= limit, err
}
func (s *Store) LinkToken(ctx context.Context, id int64, hash string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO telegram_links(token_hash,user_id,expires_at) VALUES($2,$1,now()+interval '10 minutes') ON CONFLICT(user_id) DO UPDATE SET token_hash=$2,expires_at=EXCLUDED.expires_at`, id, hash)
	return err
}
func (s *Store) BindTelegram(ctx context.Context, hash string, chat int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `DELETE FROM telegram_links WHERE token_hash=$1 AND expires_at>now() RETURNING user_id`, hash).Scan(&id)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE users SET telegram_chat_id=$2 WHERE id=$1 AND enabled`, id, chat)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `DELETE FROM deliveries WHERE user_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) UnlinkTelegram(ctx context.Context, id int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE users SET telegram_chat_id=NULL,notify_enabled=FALSE WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM telegram_links WHERE user_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM deliveries WHERE user_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) SubscriptionIDs(ctx context.Context, id int64) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT profile_id FROM subscriptions WHERE user_id=$1 ORDER BY profile_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var v int64
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		ids = append(ids, v)
	}
	return ids, rows.Err()
}
func (s *Store) SavePreferences(ctx context.Context, id int64, enabled, all bool, ids []int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE users SET notify_enabled=$2,all_profiles=$3 WHERE id=$1`, id, enabled, all); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM subscriptions WHERE user_id=$1`, id); err != nil {
		return err
	}
	for _, pid := range ids {
		if _, err = tx.Exec(ctx, `INSERT INTO subscriptions(user_id,profile_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, pid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) Recipients(ctx context.Context, pid *int64) ([]User, error) {
	rows, err := s.db.Query(ctx, `SELECT `+userCols+` FROM users WHERE enabled AND notify_enabled AND telegram_chat_id IS NOT NULL AND (all_profiles OR EXISTS(SELECT 1 FROM subscriptions WHERE user_id=users.id AND profile_id=$1))`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) TelegramUser(ctx context.Context, chat int64) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE enabled AND telegram_chat_id=$1`, chat))
}
func (s *Store) Delivery(ctx context.Context, uid, oid, chat int64) (int64, error) {
	var mid int64
	err := s.db.QueryRow(ctx, `SELECT message_id FROM deliveries WHERE user_id=$1 AND order_id=$2 AND chat_id=$3`, uid, oid, chat).Scan(&mid)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return mid, err
}
func (s *Store) SaveDelivery(ctx context.Context, uid, oid, chat, mid int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO deliveries(user_id,order_id,chat_id,message_id) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,order_id) DO UPDATE SET chat_id=$3,message_id=$4`, uid, oid, chat, mid)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE orders SET status='notified',notified_at=$2 WHERE id=$1`, oid, time.Now())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) SaveBot(ctx context.Context, value string, reset bool) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO kv(key,value) VALUES('telegram_bot',$1) ON CONFLICT(key) DO UPDATE SET value=$1`, value)
	if err != nil {
		return err
	}
	if reset {
		for _, q := range []string{`UPDATE users SET telegram_chat_id=NULL,notify_enabled=FALSE`, `DELETE FROM telegram_links`, `DELETE FROM deliveries`} {
			if _, err = tx.Exec(ctx, q); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}
