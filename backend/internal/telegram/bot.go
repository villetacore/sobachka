// Package telegram — минимальный клиент Telegram Bot API без сторонних библиотек.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type Bot struct {
	token string
	hc    *http.Client
}

func New(token string) *Bot {
	return &Bot{token: token, hc: &http.Client{Timeout: 70 * time.Second, Transport: http.DefaultTransport}}
}

type Button struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type TelegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

func (b *Bot) GetMe(ctx context.Context) (TelegramUser, error) {
	var u TelegramUser
	err := b.call(ctx, "getMe", nil, &u)
	return u, err
}

type Message struct {
	From      *TelegramUser `json:"from"`
	MessageID int64         `json:"message_id"`
	Chat      Chat          `json:"chat"`
	Text      string        `json:"text"`
}

type CallbackQuery struct {
	From    TelegramUser `json:"from"`
	ID      string       `json:"id"`
	Data    string       `json:"data"`
	Message *Message     `json:"message"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

func (b *Bot) call(ctx context.Context, method string, payload, out any) error {
	if method != "getUpdates" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+b.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return b.hide(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.hc.Do(req)
	if err != nil {
		return b.hide(err)
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	if !r.OK {
		return fmt.Errorf("telegram %s: %s", method, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// hide убирает токен из текста ошибки, чтобы он не попал в логи.
func (b *Bot) hide(err error) error {
	return errors.New(strings.ReplaceAll(err.Error(), b.token, "***"))
}

func keyboard(rows [][]Button) any {
	if rows == nil {
		return nil
	}
	return map[string]any{"inline_keyboard": rows}
}

func (b *Bot) SendMessage(ctx context.Context, chatID int64, html string, rows [][]Button) (int64, error) {
	payload := map[string]any{
		"chat_id":              chatID,
		"text":                 html,
		"parse_mode":           "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true},
	}
	if rows != nil {
		payload["reply_markup"] = keyboard(rows)
	}
	var m Message
	err := b.call(ctx, "sendMessage", payload, &m)
	return m.MessageID, err
}

func (b *Bot) EditMessage(ctx context.Context, chatID, messageID int64, html string, rows [][]Button) error {
	payload := map[string]any{
		"chat_id":              chatID,
		"message_id":           messageID,
		"text":                 html,
		"parse_mode":           "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true},
	}
	if rows != nil {
		payload["reply_markup"] = keyboard(rows)
	}
	err := b.call(ctx, "editMessageText", payload, nil)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

func (b *Bot) EditKeyboard(ctx context.Context, chatID, messageID int64, rows [][]Button) error {
	err := b.call(ctx, "editMessageReplyMarkup", map[string]any{
		"chat_id": chatID, "message_id": messageID, "reply_markup": keyboard(rows),
	}, nil)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

func (b *Bot) AnswerCallback(ctx context.Context, id, text string) error {
	return b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

// Poll читает обновления long polling'ом, пока не отменён ctx.
func (b *Bot) Poll(ctx context.Context, handle func(context.Context, Update)) {
	var offset int64
	for ctx.Err() == nil {
		var updates []Update
		err := b.call(ctx, "getUpdates", map[string]any{
			"offset":          offset,
			"timeout":         50,
			"allowed_updates": []string{"message", "callback_query"},
		}, &updates)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("telegram: getUpdates: %v", err)
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Second):
				}
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			handle(ctx, u)
		}
	}
}

func (b *Bot) CheckPolling(ctx context.Context) error {
	var info struct {
		URL string `json:"url"`
	}
	if err := b.call(ctx, "getWebhookInfo", nil, &info); err != nil {
		return err
	}
	if info.URL != "" {
		return errors.New("у бота установлен webhook; используйте отдельного бота или удалите webhook в прежней интеграции")
	}
	return nil
}
