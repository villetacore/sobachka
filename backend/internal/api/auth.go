package api

import (
	"context"
	"errors"
	"net/http"
	"sobachka/internal/security"
	"sobachka/internal/store"
	"strings"
	"time"
)

type userKey struct{}

func current(r *http.Request) store.User { return r.Context().Value(userKey{}).(store.User) }

const sessionCookie = "sobachka_session"

// A custom header on every mutation, JSON-only bodies, no CORS and strict cookies
// prevent cross-origin forms/fetches from exercising a user's session.
func validMutation(r *http.Request) bool {
	if r.Method == "GET" || r.Method == "HEAD" {
		return true
	}
	return r.Header.Get("X-Sobachka-Request") == "1" && strings.Split(r.Header.Get("Content-Type"), ";")[0] == "application/json" && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}
func allowed(role, method, path string) bool {
	if role == "admin" {
		return true
	}
	if path == "/api/auth" || strings.HasPrefix(path, "/api/auth/") || strings.HasPrefix(path, "/api/me/") || path == "/api/telegram/bot" && method == "GET" {
		return true
	}
	if method == "GET" && (path == "/api/stats" || path == "/api/profiles" || path == "/api/orders" || strings.HasPrefix(path, "/api/orders/")) {
		return true
	}
	if role == "operator" {
		if strings.HasPrefix(path, "/api/profiles") {
			return true
		}
		if method == "POST" && strings.HasPrefix(path, "/api/orders/") {
			return true
		}
	}
	return false
}
func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/api/health" && r.Method == "GET" {
			next.ServeHTTP(w, r)
			return
		}
		if !validMutation(r) {
			writeErr(w, 403, errors.New("запрос не прошёл CSRF-проверку"))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != a.cfg.PublicURL {
			writeErr(w, 403, errors.New("недопустимый Origin"))
			return
		}
		if r.URL.Path == "/api/auth/login" && r.Method == "POST" {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || len(c.Value) != 43 {
			writeErr(w, 401, errors.New("войдите в аккаунт"))
			return
		}
		u, err := a.st.Session(r.Context(), security.Digest(c.Value))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, 401, errors.New("сессия истекла, войдите снова"))
			} else {
				fail(w, err)
			}
			return
		}
		if !allowed(u.Role, r.Method, r.URL.Path) {
			writeErr(w, 403, errors.New("недостаточно прав"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}
func (a *API) cookie(w http.ResponseWriter, value string, age int) {
	c := &http.Cookie{Name: sessionCookie, Value: value, Path: "/api", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: age}
	if age < 0 {
		c.Expires = time.Unix(1, 0)
	}
	http.SetCookie(w, c)
}

// Valid bcrypt hash used to keep unknown-account verification comparable in cost.
const dummyHash = "$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW"

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if decode(r, &b) != nil || len(b.Username) > 64 || len(b.Password) > 72 {
		writeErr(w, 400, errors.New("некорректные данные входа"))
		return
	}
	// A global budget also bounds password-hashing work for random usernames.
	for _, bucket := range []struct {
		key   string
		limit int
	}{{"global", 100}, {"user:" + security.Digest(security.Login(b.Username)), 10}} {
		key, limit := bucket.key, bucket.limit
		ok, err := a.st.AllowLogin(r.Context(), key, limit)
		if err != nil {
			fail(w, err)
			return
		}
		if !ok {
			w.Header().Set("Retry-After", "900")
			writeErr(w, 429, errors.New("слишком много попыток; повторите через 15 минут"))
			return
		}
	}
	u, err := a.st.UserByLogin(r.Context(), security.Login(b.Username))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		fail(w, err)
		return
	}
	hash := u.PasswordHash
	if err != nil {
		hash = dummyHash
	}
	valid := security.Verify(hash, b.Password)
	if !valid || err != nil || !u.Enabled {
		writeErr(w, 401, errors.New("неверный логин или пароль"))
		return
	}
	token := security.Token()
	if err = a.st.NewSession(r.Context(), u, security.Digest(token)); err != nil {
		fail(w, err)
		return
	}
	a.cookie(w, token, 12*60*60)
	writeJSON(w, u)
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookie)
	if err := a.st.DeleteSession(r.Context(), security.Digest(c.Value)); err != nil {
		fail(w, err)
		return
	}
	a.cookie(w, "", -1)
	writeJSON(w, map[string]bool{"ok": true})
}
func (a *API) password(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if decode(r, &b) != nil {
		writeErr(w, 400, errors.New("некорректные данные"))
		return
	}
	u := current(r)
	ok, err := a.st.AllowLogin(r.Context(), "password:"+security.Digest(u.Username), 10)
	if err != nil {
		fail(w, err)
		return
	}
	if !ok {
		writeErr(w, 429, errors.New("слишком много попыток"))
		return
	}
	if !security.Verify(u.PasswordHash, b.Current) {
		writeErr(w, 400, errors.New("неверный текущий пароль"))
		return
	}
	hash, err := security.Password(b.Password)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err = a.st.ChangePassword(r.Context(), u.ID, u.PasswordHash, hash); err != nil {
		fail(w, err)
		return
	}
	a.cookie(w, "", -1)
	writeJSON(w, map[string]bool{"ok": true})
}
func (a *API) users(w http.ResponseWriter, r *http.Request) {
	u, err := a.st.Users(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, u)
}
func (a *API) saveUser(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Enabled  bool   `json:"enabled"`
	}
	if decode(r, &b) != nil || !security.ValidRole(b.Role) {
		writeErr(w, 400, errors.New("некорректные данные пользователя или роль"))
		return
	}
	u := store.User{Username: security.Login(b.Username), Role: b.Role, Enabled: b.Enabled}
	var err error
	if b.Password != "" || r.Method == "POST" {
		u.PasswordHash, err = security.Password(b.Password)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
	}
	if r.Method == "POST" {
		if !security.ValidLogin(u.Username) {
			writeErr(w, 400, errors.New("логин: 3–64 символа a-z, 0-9, _, ., -"))
			return
		}
		u, err = a.st.CreateUser(r.Context(), u)
		if err != nil {
			writeErr(w, 400, errors.New("не удалось создать пользователя; возможно, логин занят"))
			return
		}
	} else {
		u.ID, err = pathID(r)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if err = a.st.UpdateUser(r.Context(), u); err != nil {
			writeErr(w, 400, errors.New("не удалось обновить пользователя; должен остаться активный администратор"))
			return
		}
		u, err = a.st.User(r.Context(), u.ID)
		if err != nil {
			fail(w, err)
			return
		}
	}
	writeJSON(w, u)
}
func (a *API) preferences(w http.ResponseWriter, r *http.Request) {
	ids, err := a.st.SubscriptionIDs(r.Context(), current(r).ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"user": current(r), "profile_ids": ids})
}
func (a *API) savePreferences(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled bool    `json:"notify_enabled"`
		All     bool    `json:"all_profiles"`
		IDs     []int64 `json:"profile_ids"`
	}
	if decode(r, &b) != nil || len(b.IDs) > 500 {
		writeErr(w, 400, errors.New("некорректные настройки"))
		return
	}
	if err := a.st.SavePreferences(r.Context(), current(r).ID, b.Enabled, b.All, b.IDs); err != nil {
		writeErr(w, 400, errors.New("не удалось сохранить: проверьте выбранные профили"))
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
func (a *API) botInfo(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.svc.BotInfo()) }
func (a *API) configureBot(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Token string `json:"token"`
	}
	if decode(r, &b) != nil {
		writeErr(w, 400, errors.New("некорректные данные"))
		return
	}
	if err := a.svc.ConfigureBot(r.Context(), strings.TrimSpace(b.Token)); err != nil {
		writeErr(w, 400, err)
		return
	}
	a.botInfo(w, r)
}
func (a *API) linkTelegram(w http.ResponseWriter, r *http.Request) {
	link, err := a.svc.LinkTelegram(r.Context(), current(r).ID)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, map[string]any{"url": link, "expires_in": 600})
}
func (a *API) unlinkTelegram(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.UnlinkTelegram(r.Context(), current(r).ID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
