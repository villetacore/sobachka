package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sobachka/internal/config"
	"sobachka/internal/core"
	"sobachka/internal/ml"
	"sobachka/internal/security"
	"sobachka/internal/store"
	"sobachka/internal/testdb"
	"strings"
	"testing"
)

func TestAccessPolicy(t *testing.T) {
	for _, tc := range []struct {
		role, method, path string
		want               bool
	}{
		{"viewer", "GET", "/api/orders", true}, {"viewer", "POST", "/api/orders/1/feedback", false},
		{"viewer", "GET", "/api/sources", false}, {"operator", "GET", "/api/sources", false},
		{"operator", "PUT", "/api/settings", false}, {"operator", "POST", "/api/users", false},
		{"operator", "PUT", "/api/telegram/bot", false}, {"operator", "PUT", "/api/profiles/1", true},
		{"viewer", "PUT", "/api/me/notifications", true}, {"admin", "POST", "/api/users", true},
	} {
		if got := allowed(tc.role, tc.method, tc.path); got != tc.want {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}
func TestAnonymousFailClosed(t *testing.T) {
	h := New(config.Config{}, nil, nil, nil).Handler()
	for _, path := range []string{"/api/auth", "/api/orders", "/api/users", "/api/settings"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer old-admin-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("%s = %d", path, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing CSRF header accepted")
	}
}
func TestAuthLifecycle(t *testing.T) {
	st, db := testdb.Open(t)
	ctx := context.Background()
	if err := st.Bootstrap(ctx, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	hash, err := security.Password("viewer-password-123")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := st.CreateUser(ctx, store.User{Username: "viewer", PasswordHash: hash, Role: "viewer", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{PublicURL: "https://test.example", CookieSecure: true}
	svc := core.New(cfg, st, ml.New("http://127.0.0.1:1"), nil)
	h := New(cfg, st, svc, nil).Handler()
	req := func(method, path, body string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Sobachka-Request", "1")
		r.Header.Set("Origin", origin)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := func(name, password string) *http.Cookie {
		w := req("POST", "/api/auth/login", `{"username":"`+name+`","password":"`+password+`"}`, nil, cfg.PublicURL)
		if w.Code != 200 {
			t.Fatalf("login: %d %s", w.Code, w.Body.String())
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatal("no session cookie")
		}
		c := cookies[0]
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 43200 {
			t.Fatal("insecure session cookie")
		}
		if strings.Contains(w.Body.String(), "password") {
			t.Fatal("hash leaked")
		}
		return c
	}
	admin := login("admin", "admin-password-123")
	vc := login("viewer", "viewer-password-123")
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/orders", "", 200}, {"GET", "/api/users", "", 403}, {"GET", "/api/settings", "", 403},
		{"PUT", "/api/telegram/bot", `{"token":"x"}`, 403}, {"POST", "/api/orders/1/feedback", `{"value":1}`, 403},
		{"PUT", "/api/me/notifications", `{"notify_enabled":true,"all_profiles":true,"user_id":1}`, 200},
	} {
		w := req(tc.method, tc.path, tc.body, vc, cfg.PublicURL)
		if w.Code != tc.want {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	a, _ := st.UserByLogin(ctx, "admin")
	v, _ := st.User(ctx, viewer.ID)
	if a.NotifyEnabled || !v.NotifyEnabled {
		t.Fatal("preferences modified another user")
	}
	w := req("POST", "/api/auth/logout", `{}`, vc, "https://evil.example")
	if w.Code != 403 {
		t.Fatal("cross-origin request accepted")
	}
	w = req("POST", "/api/auth/logout", `{}`, vc, cfg.PublicURL)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = req("GET", "/api/auth", "", vc, cfg.PublicURL); w.Code != 401 {
		t.Fatal("logout didn't revoke cookie")
	}
	vc = login("viewer", "viewer-password-123")
	w = req("POST", "/api/auth/password", `{"current_password":"viewer-password-123","password":"new-password-456"}`, vc, cfg.PublicURL)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = req("GET", "/api/auth", "", vc, cfg.PublicURL); w.Code != 401 {
		t.Fatal("password change didn't revoke cookie")
	}
	vc = login("viewer", "new-password-456")
	body, _ := json.Marshal(map[string]any{"role": "operator", "enabled": false})
	w = req("PUT", "/api/users/"+jsonNumber(viewer.ID), string(body), admin, cfg.PublicURL)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = req("GET", "/api/auth", "", vc, cfg.PublicURL); w.Code != 401 {
		t.Fatal("blocked user still authenticated")
	}
	if _, err = db.Exec(ctx, `UPDATE sessions SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if w = req("GET", "/api/auth", "", admin, cfg.PublicURL); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
	// No credential value distinguishes disabled accounts from unknown accounts.
	disabled := req("POST", "/api/auth/login", `{"username":"viewer","password":"new-password-456"}`, nil, cfg.PublicURL)
	unknown := req("POST", "/api/auth/login", `{"username":"nobody","password":"wrong-password"}`, nil, cfg.PublicURL)
	if disabled.Code != 401 || disabled.Body.String() != unknown.Body.String() {
		t.Fatal("account enumeration")
	}
	for i := 0; i < 11; i++ {
		w = req("POST", "/api/auth/login", `{"username":"limited","password":"wrong-password"}`, nil, cfg.PublicURL)
	}
	if w.Code != 429 {
		t.Fatalf("rate limit = %d", w.Code)
	}
}
func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
