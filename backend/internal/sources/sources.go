// Package sources содержит парсеры фриланс-бирж.
// Чтобы добавить новую биржу, реализуйте Fetcher и зарегистрируйте его в Kinds.
package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"sobachka/internal/store"
)

type Fetcher interface {
	Fetch(ctx context.Context, src store.Source) ([]store.RawOrder, error)
}

// Detailer — источник, который умеет догрузить описание со страницы заказа.
type Detailer interface {
	Details(ctx context.Context, src store.Source, url string) (description, budget string, budgetValue *int, err error)
}

type Kind struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Hint       string `json:"hint"`
	ExampleURL string `json:"example_url"`
}

var Kinds = []Kind{
	{ID: "rss", Title: "RSS / Atom лента", Hint: "Любая лента заказов", ExampleURL: "https://example.com/projects.rss"},
	{ID: "html", Title: "HTML-страница со списком заказов", Hint: "Страница со списком и регулярное выражение для ссылок на заказы, например /task/[0-9]+. " +
		"Если на странице не только заказы, в настройках задайте card (начало карточки) и include (что должно быть в карточке заказа). " +
		"Для страниц со входом вставьте Cookie из браузера", ExampleURL: "https://example.com/tasks/"},
	{ID: "json", Title: "JSON API", Hint: "Адрес API, из которого сайт берёт заказы. В настройках: items — путь к списку, fields — где в заказе " +
		"заголовок, ссылка, описание, бюджет; при необходимости method, form, page_param, pages, filter", ExampleURL: "https://example.com/api/tasks"},
}

// Preset — готовая настройка площадки для кнопки «Готовые площадки». В репозитории их нет:
// список читается из файла PRESETS_FILE (JSON-массив), который у каждой установки свой.
type Preset struct {
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	URL         string          `json:"url"`
	Pattern     string          `json:"pattern,omitempty"`
	IntervalSec int             `json:"interval_sec"`
	Note        string          `json:"note,omitempty"`
	Config      json.RawMessage `json:"config,omitempty"`
}

// LoadPresets читает готовые площадки из файла. Нет файла — нет готовых площадок.
func LoadPresets(path string) ([]Preset, error) {
	out := []Preset{}
	if path == "" {
		return out, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return []Preset{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, p := range out {
		if _, err := New(p.Kind, nil, ""); err != nil {
			return []Preset{}, fmt.Errorf("%s: %q: %w", path, p.Name, err)
		}
	}
	return out, nil
}

func New(kind string, client *http.Client, userAgent string) (Fetcher, error) {
	switch kind {
	case "rss":
		return &RSS{client: client, ua: userAgent}, nil
	case "html":
		return &HTML{client: client, ua: userAgent}, nil
	case "json":
		return &JSON{client: client, ua: userAgent}, nil
	}
	return nil, fmt.Errorf("неизвестный тип источника %q", kind)
}

func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
}

var (
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	brRe    = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li)\s*/?>`)
	spaceRe = regexp.MustCompile(`[ \t\x{00a0}]+`)
	nlRe    = regexp.MustCompile(`\n{3,}`)
	// «Бюджет: 15 000 ₽», «бюджет 500$», «Бюджет: 3000 руб.»
	budgetRe = regexp.MustCompile(`(?i)бюджет\s*:?\s*(?:до\s*)?([\d][\d \x{00a0}]*)\s*(₽|руб\.?|р\.|\$|usd|€|eur)?`)
	// Бюджет в конце заголовка: «Название - 4000UAH».
	suffixBudgetRe = regexp.MustCompile(`(?i)\s-\s*([\d][\d \x{00a0}]*)\s*(uah|usd|eur|rub|грн|₴|\$|€|₽)\s*$`)
)

// cleanText превращает HTML в читаемый текст.
func cleanText(s string) string {
	s = brRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = spaceRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	s = nlRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(s)
}

// extractBudget ищет в тексте упоминание бюджета.
func extractBudget(texts ...string) (string, *int) {
	for _, t := range texts {
		m := budgetRe.FindStringSubmatch(t)
		if m == nil {
			m = suffixBudgetRe.FindStringSubmatch(t)
		}
		if m == nil {
			continue
		}
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, m[1])
		n, err := strconv.Atoi(digits)
		if err != nil || n == 0 {
			continue
		}
		cur := strings.TrimSpace(m[2])
		switch strings.ToLower(cur) {
		case "", "rub", "руб", "руб.", "р.":
			cur = "₽"
		case "uah", "грн":
			cur = "₴"
		}
		return fmt.Sprintf("%d %s", n, cur), &n
	}
	return "", nil
}
