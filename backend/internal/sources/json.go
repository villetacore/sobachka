package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sobachka/internal/store"
)

// JSON читает заказы из JSON API площадки. Как найти список и поля заказа, задаётся в
// настройках источника (Config): так подключается любой сайт, чей фронтенд берёт заказы из API.
type JSON struct {
	client *http.Client
	ua     string
}

func (f *JSON) page(ctx context.Context, src store.Source, c Config, page int) ([]any, error) {
	u, err := url.Parse(src.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("некорректный адрес API")
	}
	method := strings.ToUpper(c.Method)
	if method == "" {
		method = http.MethodGet
	}
	form := url.Values{}
	for k, v := range c.Form {
		form.Set(k, v)
	}
	if c.PageParam != "" {
		if method == http.MethodPost {
			form.Set(c.PageParam, strconv.Itoa(page))
		} else {
			q := u.Query()
			q.Set(c.PageParam, strconv.Itoa(page))
			u.RawQuery = q.Encode()
		}
	}
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.ua)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	if src.Cookie != "" {
		req.Header.Set("Cookie", src.Cookie)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	data, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("ответ не JSON: %w", err)
	}
	items, ok := lookup(doc, c.Items).([]any)
	if !ok {
		return nil, fmt.Errorf("в ответе нет списка по пути %q", c.Items)
	}
	return items, nil
}

func (f *JSON) Fetch(ctx context.Context, src store.Source) ([]store.RawOrder, error) {
	c, err := parseConfig(src)
	if err != nil {
		return nil, err
	}
	pages := 1
	if c.PageParam != "" && c.Pages > 1 {
		pages = min(c.Pages, 20)
	}
	var items []any
	for p := 1; p <= pages; p++ {
		got, err := f.page(ctx, src, c, p)
		if err != nil {
			if p == 1 {
				return nil, err
			}
			break
		}
		items = append(items, got...)
		if len(got) == 0 {
			break
		}
	}

	fl := c.Fields
	var out []store.RawOrder
next:
	for _, it := range items {
		for k, want := range c.Filter {
			if text(lookup(it, k)) != want {
				continue next
			}
		}
		title, link := cleanText(field(it, fl.Title)), strings.TrimSpace(field(it, fl.URL))
		if title == "" || link == "" {
			continue
		}
		if base, err := url.Parse(src.URL); err == nil {
			if u, err := base.Parse(link); err == nil {
				link = u.String()
			}
		}
		id := field(it, fl.ID)
		if id == "" {
			id = link
		}
		budget, value := jsonBudget(it, fl)
		out = append(out, store.RawOrder{
			ExternalID:  id,
			URL:         link,
			Title:       title,
			Description: cleanText(field(it, fl.Description)),
			Category:    cleanText(field(it, fl.Category)),
			Budget:      budget,
			BudgetValue: value,
			PublishedAt: parseDate(field(it, fl.Published), fl.TimeFormat, fl.Timezone),
		})
	}
	return out, nil
}

// lookup достаёт значение по пути "a.b.0.c"; варианты через «|» — берётся первый непустой.
func lookup(v any, path string) any {
	for _, alt := range strings.Split(path, "|") {
		cur := v
		for _, key := range strings.Split(strings.TrimSpace(alt), ".") {
			if key == "" {
				continue
			}
			switch x := cur.(type) {
			case map[string]any:
				cur = x[key]
			case []any:
				i, err := strconv.Atoi(key)
				if err != nil || i < 0 || i >= len(x) {
					cur = nil
				} else {
					cur = x[i]
				}
			default:
				cur = nil
			}
		}
		if cur != nil && text(cur) != "" || isList(cur) {
			return cur
		}
	}
	return nil
}

func isList(v any) bool { _, ok := v.([]any); return ok }

// text превращает значение JSON в строку: числа без «.0», списки — через запятую.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			if s := text(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

var placeholderRe = regexp.MustCompile(`\{([^{}]+)\}`)

// field — значение поля: путь ("name") или шаблон с подстановками ("{type} / {tags}").
func field(item any, spec string) string {
	if spec == "" {
		return ""
	}
	if !strings.Contains(spec, "{") {
		return text(lookup(item, spec))
	}
	s := placeholderRe.ReplaceAllStringFunc(spec, func(m string) string {
		return text(lookup(item, m[1:len(m)-1]))
	})
	// Пустые подстановки оставляют висящие разделители: «a /  / b» → «a / b», «a / » → «a».
	s = strings.TrimSpace(strings.ReplaceAll(s, " /  / ", " / "))
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "/ "), " /"))
}

func number(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		f, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), " ", ""), 64)
		return int(f)
	}
	return 0
}

func jsonBudget(item any, fl Fields) (string, *int) {
	cur := strings.TrimSpace(field(item, fl.Currency))
	if cur == "" && fl.Currency != "" && !strings.Contains(fl.Currency, "{") && lookup(item, fl.Currency) == nil {
		cur = fl.Currency // не поле, а сама валюта, например "₽"
	}
	switch strings.ToLower(cur) {
	case "", "rub", "rur", "руб", "руб.":
		cur = "₽"
	case "usd":
		cur = "$"
	case "eur":
		cur = "€"
	}
	var fixed, lo, hi int
	if fl.Budget != "" {
		fixed = number(lookup(item, fl.Budget))
	}
	if fl.BudgetMin != "" {
		lo = number(lookup(item, fl.BudgetMin))
	}
	if fl.BudgetMax != "" {
		hi = number(lookup(item, fl.BudgetMax))
	}
	switch {
	case fixed > 0:
		return fmt.Sprintf("%d %s", fixed, cur), &fixed
	case lo > 0 && hi > lo:
		return fmt.Sprintf("%d–%d %s", lo, hi, cur), &hi
	case hi > 0:
		return fmt.Sprintf("до %d %s", hi, cur), &hi
	case lo > 0:
		return fmt.Sprintf("от %d %s", lo, cur), &lo
	}
	return "", nil
}

// parseDate понимает RFC 3339, «2006-01-02 15:04:05» и формат из настроек.
func parseDate(s, layout, tz string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	loc := time.UTC
	if tz != "" {
		if t, err := time.Parse("-07:00", tz); err == nil {
			loc = t.Location()
		} else if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	layouts := []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"}
	if layout != "" {
		layouts = append([]string{layout}, layouts...)
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return &t
		}
	}
	return nil
}
