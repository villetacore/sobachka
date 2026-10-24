package store_test

import (
	"context"
	"sobachka/internal/security"
	"sobachka/internal/store"
	"sobachka/internal/testdb"
	"testing"
)

func TestUsersSessionsAndTelegram(t *testing.T) {
	st, db := testdb.Open(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.Bootstrap(ctx, "admin", "initial-password"))
	must(st.Bootstrap(ctx, "ignored", ""))
	admin, err := st.UserByLogin(ctx, "admin")
	must(err)
	admin.Enabled = false
	if st.UpdateUser(ctx, admin) == nil {
		t.Fatal("disabled last admin")
	}
	admin.Enabled = true
	must(st.NewSession(ctx, admin, "session"))
	if _, err = st.Session(ctx, "session"); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `UPDATE sessions SET expires_at=now()-interval '1 second'`)
	must(err)
	if _, err = st.Session(ctx, "session"); err == nil {
		t.Fatal("expired session accepted")
	}
	must(st.NewSession(ctx, admin, "fresh"))
	must(st.ChangePassword(ctx, admin.ID, admin.PasswordHash, "new-hash"))
	if _, err = st.Session(ctx, "fresh"); err == nil {
		t.Fatal("reset did not revoke session")
	}
	if st.NewSession(ctx, admin, "racing-old-password") == nil {
		t.Fatal("stale login recreated session after reset")
	}
	hash, err := security.Password("user-password-123")
	must(err)
	u, err := st.CreateUser(ctx, store.User{Username: "viewer", PasswordHash: hash, Role: "viewer", Enabled: true})
	must(err)
	must(st.LinkToken(ctx, u.ID, "old"))
	must(st.LinkToken(ctx, u.ID, "new"))
	if st.BindTelegram(ctx, "old", 101) == nil {
		t.Fatal("replaced link accepted")
	}
	must(st.BindTelegram(ctx, "new", 101))
	if st.BindTelegram(ctx, "new", 102) == nil {
		t.Fatal("replayed link accepted")
	}
	must(st.LinkToken(ctx, admin.ID, "admin-link"))
	if st.BindTelegram(ctx, "admin-link", 101) == nil {
		t.Fatal("stole another user's chat")
	}
	must(st.LinkToken(ctx, u.ID, "expired"))
	_, err = db.Exec(ctx, `UPDATE telegram_links SET expires_at=now()-interval '1 second' WHERE user_id=$1`, u.ID)
	must(err)
	if st.BindTelegram(ctx, "expired", 102) == nil {
		t.Fatal("expired link accepted")
	}
	p, err := st.CreateProfile(ctx, store.Profile{Name: "test", Enabled: true})
	must(err)
	must(st.SavePreferences(ctx, u.ID, true, false, []int64{p.ID}))
	recipients, err := st.Recipients(ctx, &p.ID)
	must(err)
	if len(recipients) != 1 || recipients[0].ID != u.ID {
		t.Fatal("subscription selection", recipients)
	}
	other := p.ID + 1000
	recipients, err = st.Recipients(ctx, &other)
	must(err)
	if len(recipients) != 0 {
		t.Fatal("unsubscribed recipient")
	}
	must(st.NewSession(ctx, u, "user-session"))
	u.Enabled = false
	must(st.UpdateUser(ctx, u))
	if _, err = st.Session(ctx, "user-session"); err == nil {
		t.Fatal("disabled user session active")
	}
	recipients, err = st.Recipients(ctx, &p.ID)
	must(err)
	if len(recipients) != 0 {
		t.Fatal("disabled recipient")
	}
	must(st.SaveBot(ctx, `{}`, true))
	admin, err = st.User(ctx, admin.ID)
	must(err)
	u, err = st.User(ctx, u.ID)
	must(err)
	if admin.TelegramChatID != nil || u.TelegramChatID != nil || u.NotifyEnabled {
		t.Fatal("bot replacement didn't reset bindings")
	}
	for i := 0; i < 3; i++ {
		ok, err := st.AllowLogin(ctx, "limit", 2)
		must(err)
		if ok != (i < 2) {
			t.Fatal("rate limit", i)
		}
	}
}
