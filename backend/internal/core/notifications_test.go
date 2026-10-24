package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sobachka/internal/config"
	"sobachka/internal/security"
	"sobachka/internal/store"
	"sobachka/internal/telegram"
	"sobachka/internal/testdb"
	"strings"
	"sync"
	"testing"
)

type telegramTransport struct {
	mu       sync.Mutex
	sends    map[int64]int
	failChat int64
	botID    int64
}

func (f *telegramTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/getUpdates") {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	result := `true`
	if strings.HasSuffix(r.URL.Path, "/getWebhookInfo") {
		result = `{"url":""}`
	}
	if strings.HasSuffix(r.URL.Path, "/getMe") {
		id := f.botID
		if id == 0 {
			id = 77
		}
		result = fmt.Sprintf(`{"id":%d,"is_bot":true,"username":"test_bot","first_name":"Test"}`, id)
	}
	if strings.HasSuffix(r.URL.Path, "/sendMessage") {
		var p struct {
			ChatID int64 `json:"chat_id"`
		}
		json.NewDecoder(r.Body).Decode(&p)
		if p.ChatID == f.failChat {
			return nil, fmt.Errorf("simulated unavailable chat")
		}
		f.sends[p.ChatID]++
		result = fmt.Sprintf(`{"message_id":%d}`, f.sends[p.ChatID])
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":` + result + `}`)), Request: r}, nil
}
func TestPersonalDeliveryAndCallbacks(t *testing.T) {
	st, _ := testdb.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &telegramTransport{sends: map[int64]int{}, failChat: 102}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = previous }()
	cfg := config.Config{SecretKey: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	s := New(cfg, st, nil, nil)
	if err := s.InitTelegram(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBot(ctx, "test-token"); err != nil {
		t.Fatal(err)
	}
	defer func() { s.botCancel() }()
	raw, _ := st.GetKV(ctx, "telegram_bot")
	if strings.Contains(raw, "test-token") {
		t.Fatal("plaintext token in database")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := st.CreateProfile(ctx, store.Profile{Name: "test", Enabled: true})
	must(err)
	src, err := st.CreateSource(ctx, store.Source{Name: "test", Kind: "rss", URL: "https://example.com"})
	must(err)
	id, _, err := st.InsertOrder(ctx, src.ID, store.RawOrder{ExternalID: "one", Title: "Test", URL: "https://example.com/order"})
	must(err)
	score := 0.9
	must(st.SetOrderScore(ctx, id, &p.ID, &score, nil, "matched"))
	users := []store.User{}
	for i := 0; i < 3; i++ {
		u, err := st.CreateUser(ctx, store.User{Username: fmt.Sprintf("user%d", i), PasswordHash: "unused", Role: "operator", Enabled: true})
		must(err)
		token := security.Token()
		must(st.LinkToken(ctx, u.ID, security.Digest(token)))
		must(st.BindTelegram(ctx, security.Digest(token), int64(101+i)))
		must(st.SavePreferences(ctx, u.ID, i < 2, true, nil))
		users = append(users, u)
	}
	if err = s.Notify(ctx, id); err == nil {
		t.Fatal("failed recipient not reported")
	}
	transport.failChat = 0
	must(s.Notify(ctx, id))
	if transport.sends[101] != 1 || transport.sends[102] != 1 || transport.sends[103] != 0 {
		t.Fatal("duplicate or wrong recipient", transport.sends)
	}
	must(s.NotifyUser(ctx, id, users[0].ID))
	if transport.sends[101] != 1 || transport.sends[102] != 1 {
		t.Fatal("manual resend broadcast")
	}
	cb := telegram.CallbackQuery{ID: "cb", From: telegram.TelegramUser{ID: 101}, Data: fmt.Sprintf("fb:%d:1", id), Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: 101, Type: "private"}}}
	cb.From.ID = 999
	s.handleUpdate(ctx, telegram.Update{CallbackQuery: &cb})
	o, _ := st.GetOrder(ctx, id)
	if o.Feedback != 0 {
		t.Fatal("foreign callback accepted")
	}
	cb.From.ID = 101
	cb.Message.MessageID = 999
	s.handleUpdate(ctx, telegram.Update{CallbackQuery: &cb})
	o, _ = st.GetOrder(ctx, id)
	if o.Feedback != 0 {
		t.Fatal("unknown message callback accepted")
	}
	cb.Message.MessageID = 1
	s.handleUpdate(ctx, telegram.Update{CallbackQuery: &cb})
	o, _ = st.GetOrder(ctx, id)
	if o.Feedback != 1 {
		t.Fatal("owner callback rejected")
	}
	users[0].Role = "viewer"
	must(st.UpdateUser(ctx, users[0]))
	cb.Data = fmt.Sprintf("fb:%d:-1", id)
	s.handleUpdate(ctx, telegram.Update{CallbackQuery: &cb})
	o, _ = st.GetOrder(ctx, id)
	if o.Feedback != 1 {
		t.Fatal("viewer modified feedback")
	}
	must(s.ConfigureBot(ctx, "rotated-token"))
	retained, err := st.User(ctx, users[0].ID)
	must(err)
	if retained.TelegramChatID == nil {
		t.Fatal("same bot token rotation lost binding")
	}
	transport.botID = 78
	must(s.ConfigureBot(ctx, "different-bot-token"))
	reset, err := st.User(ctx, users[1].ID)
	must(err)
	if reset.TelegramChatID != nil || reset.NotifyEnabled {
		t.Fatal("different bot retained binding")
	}
	must(s.ConfigureBot(ctx, ""))
	u, err := st.User(ctx, users[0].ID)
	must(err)
	if u.TelegramChatID != nil {
		t.Fatal("bot disconnect retained chat")
	}
}
