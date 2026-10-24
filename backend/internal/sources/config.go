package sources

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"sobachka/internal/store"
)

// Config — настройки разбора площадки, которые хранятся в источнике (поле config).
// Код не знает ни одной конкретной биржи: всё, чем площадки отличаются, описывается здесь.
type Config struct {
	// Дополнительные заголовки запроса (для json и html).
	Headers map[string]string `json:"headers,omitempty"`

	// ----- json -----

	// GET (по умолчанию) или POST. Для POST поля Form уходят телом x-www-form-urlencoded.
	Method string            `json:"method,omitempty"`
	Form   map[string]string `json:"form,omitempty"`
	// Имя параметра номера страницы (в query для GET, в теле для POST) и сколько страниц читать.
	PageParam string `json:"page_param,omitempty"`
	Pages     int    `json:"pages,omitempty"`
	// Путь к массиву заказов в ответе: "data.items". Варианты через «|»: "data.list|items".
	Items string `json:"items,omitempty"`
	// Оставлять только элементы, у которых поле равно значению, например {"status": "open"}.
	Filter map[string]string `json:"filter,omitempty"`

	// ----- html (режим карточек) -----

	// Регулярное выражение начала карточки заказа. Если задано, страница делится на карточки,
	// и ссылка, заголовок, дата и категория ищутся внутри каждой.
	Card string `json:"card,omitempty"`
	// Карточка берётся, только если в ней есть совпадение (например, отличает заказы от рекламы).
	Include string `json:"include,omitempty"`
	// На странице заказа описание — это текст между этими строками (иначе — мета-описание и начало текста).
	DetailsFrom string `json:"details_from,omitempty"`
	DetailsTo   string `json:"details_to,omitempty"`

	// Поля заказа. Для json — путь в элементе ("name", "user.city") или шаблон с подстановками
	// ("https://example.com/task/{id}"); варианты через «|». Для html-карточек — регулярное
	// выражение с одной группой.
	Fields Fields `json:"fields,omitempty"`
}

type Fields struct {
	ID          string `json:"id,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	Category    string `json:"category,omitempty"`
	Budget      string `json:"budget,omitempty"`
	BudgetMin   string `json:"budget_min,omitempty"`
	BudgetMax   string `json:"budget_max,omitempty"`
	Currency    string `json:"currency,omitempty"`
	Published   string `json:"published,omitempty"`
	// Формат даты в терминах Go (по умолчанию RFC 3339) и часовой пояс, например "+03:00".
	TimeFormat string `json:"time_format,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
}

func parseConfig(src store.Source) (Config, error) {
	var c Config
	if len(src.Config) == 0 || string(src.Config) == "null" {
		return c, nil
	}
	if err := json.Unmarshal(src.Config, &c); err != nil {
		return c, fmt.Errorf("настройки источника: %w", err)
	}
	return c, nil
}

// ValidateConfig проверяет настройки при сохранении источника.
func ValidateConfig(kind string, raw json.RawMessage) error {
	c, err := parseConfig(store.Source{Config: raw})
	if err != nil {
		return err
	}
	switch kind {
	case "json":
		if c.Items == "" {
			return fmt.Errorf("укажите в настройках items — путь к списку заказов в ответе")
		}
		if c.Fields.Title == "" || c.Fields.URL == "" {
			return fmt.Errorf("укажите в настройках fields.title и fields.url")
		}
		if m := strings.ToUpper(c.Method); m != "" && m != "GET" && m != "POST" {
			return fmt.Errorf("method: только GET или POST")
		}
	case "html":
		for name, re := range map[string]string{"card": c.Card, "include": c.Include, "fields.title": c.Fields.Title,
			"fields.published": c.Fields.Published, "fields.category": c.Fields.Category} {
			if re == "" {
				continue
			}
			if _, err := regexp.Compile(re); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return nil
}
