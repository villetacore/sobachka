package core

import (
	"context"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"unicode/utf8"

	"sobachka/internal/security"
	"sobachka/internal/store"
	"sobachka/internal/telegram"
)

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}

func formatOrder(o store.Order) string {
	esc := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, "🎯 <b>%s</b>\n", esc(truncate(o.Title, 200)))

	meta := []string{esc(o.SourceName)}
	if o.Budget != "" {
		meta = append(meta, "💰 "+esc(o.Budget))
	}
	if o.Score != nil {
		m := fmt.Sprintf("🧠 %.0f%%", *o.Score*100)
		if o.ProfileName != "" {
			m += " · " + esc(o.ProfileName)
		}
		meta = append(meta, m)
	}
	b.WriteString("<i>" + strings.Join(meta, " | ") + "</i>\n")
	if len(o.Matched) > 0 {
		b.WriteString("🔑 " + esc(strings.Join(firstN(o.Matched, 8), ", ")) + "\n")
	}
	b.WriteString("\n")

	if o.Description != "" {
		b.WriteString(esc(truncate(o.Description, 900)) + "\n\n")
	}
	if o.Pitch != "" {
		b.WriteString("✍️ <b>Отклик</b> (нажмите, чтобы скопировать):\n")
		b.WriteString("<pre>" + esc(truncate(o.Pitch, 1800)) + "</pre>\n")
	} else {
		b.WriteString("✍️ <i>Отклика нет — нажмите «Другой вариант».</i>\n")
	}
	fmt.Fprintf(&b, "\n🔗 %s", esc(o.URL))
	return b.String()
}

func orderKeyboard(o store.Order) [][]telegram.Button {
	up, down := "👍 Подходит", "👎 Мимо"
	switch o.Feedback {
	case 1:
		up = "✅ Подходит"
	case -1:
		down = "✅ Мимо"
	}
	id := strconv.FormatInt(o.ID, 10)
	return [][]telegram.Button{
		{{Text: "Открыть заказ", URL: o.URL}},
		{
			{Text: up, CallbackData: "fb:" + id + ":1"},
			{Text: down, CallbackData: "fb:" + id + ":-1"},
		},
		{{Text: "🔄 Другой вариант отклика", CallbackData: "rg:" + id}},
	}
}

func (s *Service) handleUpdate(ctx context.Context, u telegram.Update) {
	if m := u.Message; m != nil {
		if m.Chat.Type != "private" || m.From == nil || m.From.ID != m.Chat.ID {
			return
		}
		cmd := strings.Fields(m.Text)
		if len(cmd) > 0 && strings.Split(cmd[0], "@")[0] == "/start" {
			text := "Подключите Telegram в личных настройках веб-интерфейса: нажмите «Подключить Telegram», откройте ссылку и нажмите Start."
			if len(cmd) == 2 {
				if len(cmd[1]) != 43 || s.st.BindTelegram(ctx, security.Digest(cmd[1]), m.Chat.ID) != nil {
					text = "Ссылка истекла, уже использована или Telegram занят другим аккаунтом. Создайте новую ссылку в настройках."
				} else {
					text = "Telegram подключён. Включите уведомления и выберите направления в личных настройках 🐶"
				}
			}
			if _, err := s.bot.SendMessage(ctx, m.Chat.ID, text, nil); err != nil {
				log.Printf("telegram: %v", err)
			}
		}
		return
	}
	cb := u.CallbackQuery
	if cb == nil || cb.Message == nil {
		return
	}
	if cb.Message.Chat.Type != "private" || cb.From.ID != cb.Message.Chat.ID {
		return
	}
	user, err := s.st.TelegramUser(ctx, cb.From.ID)
	if err != nil || user.Role == "viewer" {
		_ = s.bot.AnswerCallback(ctx, cb.ID, "Нет доступа")
		return
	}
	parts := strings.Split(cb.Data, ":")
	if len(parts) < 2 {
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}

	mid, err := s.st.Delivery(ctx, user.ID, id, cb.Message.Chat.ID)
	if err != nil || mid == 0 || mid != cb.Message.MessageID {
		_ = s.bot.AnswerCallback(ctx, cb.ID, "Сообщение не связано с вашим аккаунтом")
		return
	}
	switch parts[0] {
	case "fb":
		if len(parts) != 3 {
			return
		}
		v, _ := strconv.Atoi(parts[2])
		o, err := s.st.GetOrder(ctx, id)
		if err != nil {
			_ = s.bot.AnswerCallback(ctx, cb.ID, "Заказ не найден")
			return
		}
		if o.Feedback == v {
			v = 0 // повторное нажатие снимает оценку
		}
		if err := s.SetFeedback(ctx, id, v); err != nil {
			_ = s.bot.AnswerCallback(ctx, cb.ID, "Ошибка: "+err.Error())
			return
		}
		o.Feedback = v
		_ = s.bot.AnswerCallback(ctx, cb.ID, "Учтено — нейросеть станет точнее")
		_ = s.bot.EditKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, orderKeyboard(o))

	case "rg":
		if _, err := s.Generate(ctx, id, -1); err != nil {
			_ = s.bot.AnswerCallback(ctx, cb.ID, "Ошибка: "+err.Error())
			return
		}
		_ = s.bot.AnswerCallback(ctx, cb.ID, "Готово")
		if err := s.notifyUserLocked(ctx, id, user.ID, false); err != nil {
			log.Printf("заказ %d: обновление сообщения: %v", id, err)
		}
	}
}
