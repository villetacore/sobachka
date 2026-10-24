package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sobachka/internal/security"

	"sobachka/internal/telegram"
	"time"
)

type BotConfig struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}
type storedBot struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Name       string `json:"name"`
	Ciphertext string `json:"ciphertext"`
}

func (s *Service) BotInfo() BotConfig { s.botMu.Lock(); defer s.botMu.Unlock(); return s.botInfo }
func (s *Service) HasBot() bool       { s.botMu.Lock(); defer s.botMu.Unlock(); return s.bot != nil }
func (s *Service) InitTelegram(ctx context.Context) error {
	s.botCtx = ctx
	raw, err := s.st.GetKV(ctx, "telegram_bot")
	if err != nil {
		return err
	}
	if raw != "" {
		var saved storedBot
		if err = json.Unmarshal([]byte(raw), &saved); err != nil {
			return err
		}
		if saved.Ciphertext == "" {
			return nil
		}
		token, err := security.Decrypt(s.cfg.SecretKey, saved.Ciphertext)
		if err != nil {
			return errors.New("не удалось расшифровать токен Telegram: проверьте APP_SECRET_KEY")
		}
		s.botInfo = BotConfig{ID: saved.ID, Username: saved.Username, Name: saved.Name}
		s.startBotLocked(telegram.New(token))
		return nil
	}
	if s.cfg.TelegramToken != "" {
		return s.ConfigureBot(ctx, s.cfg.TelegramToken)
	}
	return nil
}
func (s *Service) startBotLocked(bot *telegram.Bot) {
	if s.botCancel != nil {
		s.botCancel()
	}
	s.bot = bot
	if bot != nil {
		ctx, cancel := context.WithCancel(s.botCtx)
		s.botCancel = cancel
		go bot.Poll(ctx, func(c context.Context, u telegram.Update) {
			s.botMu.Lock()
			defer s.botMu.Unlock()
			if c.Err() == nil && s.bot == bot {
				s.handleUpdate(c, u)
			}
		})
	}
}
func (s *Service) ConfigureBot(ctx context.Context, token string) error {
	s.botMu.Lock()
	defer s.botMu.Unlock()
	var b *telegram.Bot
	var info BotConfig
	var ciphertext string
	if token != "" {
		if len(token) > 200 {
			return errors.New("некорректный токен бота")
		}
		var err error
		ciphertext, err = security.Encrypt(s.cfg.SecretKey, token)
		if err != nil {
			return err
		}
		b = telegram.New(token)
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		me, err := b.GetMe(check)
		if err != nil {
			return errors.New("Telegram не подтвердил токен. Проверьте токен и доступ к Telegram")
		}
		if !me.IsBot || me.Username == "" {
			return errors.New("токен не принадлежит боту")
		}
		if err := b.CheckPolling(check); err != nil {
			return errors.New("бот недоступен для polling: проверьте соединение и отключите webhook в прежней интеграции")
		}
		info = BotConfig{ID: me.ID, Username: me.Username, Name: me.FirstName}
	}
	data, _ := json.Marshal(storedBot{ID: info.ID, Username: info.Username, Name: info.Name, Ciphertext: ciphertext})
	if err := s.st.SaveBot(ctx, string(data), s.botInfo.ID != info.ID); err != nil {
		return err
	}
	s.botInfo = info
	s.startBotLocked(b)
	return nil
}
func (s *Service) LinkTelegram(ctx context.Context, uid int64) (string, error) {
	s.botMu.Lock()
	defer s.botMu.Unlock()
	if s.bot == nil {
		return "", errors.New("администратор ещё не настроил бота")
	}
	token := security.Token()
	if err := s.st.LinkToken(ctx, uid, security.Digest(token)); err != nil {
		return "", err
	}
	return "https://t.me/" + url.PathEscape(s.botInfo.Username) + "?start=" + token, nil
}
func (s *Service) UnlinkTelegram(ctx context.Context, uid int64) error {
	s.botMu.Lock()
	defer s.botMu.Unlock()
	return s.st.UnlinkTelegram(ctx, uid)
}
func (s *Service) SendTest(ctx context.Context, uid int64) error {
	s.botMu.Lock()
	defer s.botMu.Unlock()
	if s.bot == nil {
		return errors.New("бот не настроен")
	}
	u, err := s.st.User(ctx, uid)
	if err != nil {
		return err
	}
	if !u.Enabled || u.TelegramChatID == nil {
		return errors.New("сначала подключите свой Telegram")
	}
	_, err = s.bot.SendMessage(ctx, *u.TelegramChatID, "✅ Собачка на связи. Это ваши персональные уведомления.", nil)
	return err
}
func (s *Service) NotifyUser(ctx context.Context, id, uid int64) error {
	s.botMu.Lock()
	defer s.botMu.Unlock()
	return s.notifyUserLocked(ctx, id, uid, false)
}

// Delivery records are per recipient. A retry never sends twice to recipients
// already reached; manual requests update only the current user's message.
func (s *Service) notifyUserLocked(ctx context.Context, id, uid int64, automatic bool) error {
	if s.bot == nil {
		return errors.New("бот не настроен")
	}
	u, err := s.st.User(ctx, uid)
	if err != nil {
		return err
	}
	if !u.Enabled || u.TelegramChatID == nil {
		return errors.New("сначала подключите свой Telegram")
	}
	o, err := s.st.GetOrder(ctx, id)
	if err != nil {
		return err
	}
	if automatic {
		if !u.NotifyEnabled {
			return nil
		}
		if !u.AllProfiles {
			ids, err := s.st.SubscriptionIDs(ctx, uid)
			if err != nil {
				return err
			}
			found := false
			for _, pid := range ids {
				if o.ProfileID != nil && pid == *o.ProfileID {
					found = true
				}
			}
			if !found {
				return nil
			}
		}
	}
	mid, err := s.st.Delivery(ctx, uid, id, *u.TelegramChatID)
	if err != nil {
		return err
	}
	keyboard := orderKeyboard(o)
	if u.Role == "viewer" {
		keyboard = [][]telegram.Button{{{Text: "Открыть заказ", URL: o.URL}}}
	}
	if mid != 0 {
		if automatic {
			return nil
		}
		return s.bot.EditMessage(ctx, *u.TelegramChatID, mid, formatOrder(o), keyboard)
	}
	mid, err = s.bot.SendMessage(ctx, *u.TelegramChatID, formatOrder(o), keyboard)
	if err != nil {
		return err
	}
	return s.st.SaveDelivery(ctx, uid, id, *u.TelegramChatID, mid)
}
func (s *Service) Notify(ctx context.Context, id int64) error {
	s.botMu.Lock()
	defer s.botMu.Unlock()
	if s.bot == nil {
		return nil
	}
	o, err := s.st.GetOrder(ctx, id)
	if err != nil {
		return err
	}
	users, err := s.st.Recipients(ctx, o.ProfileID)
	if err != nil {
		return err
	}
	var failures []error
	for _, u := range users {
		if err := s.notifyUserLocked(ctx, id, u.ID, true); err != nil {
			failures = append(failures, fmt.Errorf("получатель %d: %w", u.ID, err))
		}
	}
	return errors.Join(failures...)
}
