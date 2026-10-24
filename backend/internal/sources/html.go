package sources

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/text/encoding/charmap"

	"sobachka/internal/store"
)

// HTML — универсальный источник: берёт со страницы-списка все ссылки, подходящие под
// регулярное выражение src.Pattern, текст ссылки становится заголовком заказа.
// В режиме карточек (Config.Card) страница сначала делится на карточки, и ссылка, заголовок,
// дата и категория ищутся внутри каждой; Config.Include отсекает карточки не-заказов.
// Описание подтягивается со страницы самого заказа (Details) только для новых заказов.
type HTML struct {
	client *http.Client
	ua     string
}

var (
	anchorRe   = regexp.MustCompile(`(?is)<a\s[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
	metaDescRe = regexp.MustCompile(`(?is)<meta\s[^>]*(?:name|property)\s*=\s*["'](?:og:description|description)["'][^>]*content\s*=\s*["']([^"']*)["']`)
	metaRevRe  = regexp.MustCompile(`(?is)<meta\s[^>]*content\s*=\s*["']([^"']*)["'][^>]*(?:name|property)\s*=\s*["'](?:og:description|description)["']`)
	titleRe    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	scriptRe   = regexp.MustCompile(`(?is)<(script|style|noscript|svg|header|footer|nav)[^>]*>.*?</(script|style|noscript|svg|header|footer|nav)>`)
	cp1251Re   = regexp.MustCompile(`(?i)charset\s*=\s*["']?(windows-1251|cp1251)`)
)

func (f *HTML) get(ctx context.Context, src store.Source, pageURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")
	if c, err := parseConfig(src); err == nil {
		for k, v := range c.Headers {
			req.Header.Set(k, v)
		}
	}
	if src.Cookie != "" {
		req.Header.Set("Cookie", src.Cookie)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	body, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	head := body
	if len(head) > 2048 {
		head = head[:2048]
	}
	if cp1251Re.MatchString(ct) || cp1251Re.Match(head) {
		body, _ = io.ReadAll(charmap.Windows1251.NewDecoder().Reader(bytes.NewReader(body)))
	}
	return body, nil
}

func (f *HTML) Fetch(ctx context.Context, src store.Source) ([]store.RawOrder, error) {
	if strings.TrimSpace(src.Pattern) == "" {
		return nil, fmt.Errorf("для html-источника нужно указать шаблон ссылок")
	}
	pattern, err := regexp.Compile(src.Pattern)
	if err != nil {
		return nil, fmt.Errorf("шаблон ссылок: %w", err)
	}
	base, err := url.Parse(src.URL)
	if err != nil {
		return nil, err
	}
	c, err := parseConfig(src)
	if err != nil {
		return nil, err
	}
	body, err := f.get(ctx, src, src.URL)
	if err != nil {
		return nil, err
	}
	if c.Card != "" {
		return f.cards(string(body), base, pattern, c)
	}

	seen := map[string]int{}
	var out []store.RawOrder
	for _, m := range anchorRe.FindAllStringSubmatch(string(body), -1) {
		href := strings.TrimSpace(m[1])
		if !pattern.MatchString(href) {
			continue
		}
		u, err := base.Parse(href)
		if err != nil {
			continue
		}
		u.Fragment = ""
		link := u.String()
		title := cleanText(m[2])
		if i, ok := seen[link]; ok {
			// Одна карточка часто содержит несколько ссылок (картинка, заголовок) — берём самый длинный текст.
			if len([]rune(title)) > len([]rune(out[i].Title)) {
				out[i].Title = title
			}
			continue
		}
		seen[link] = len(out)
		out = append(out, store.RawOrder{ExternalID: link, URL: link, Title: title})
	}
	// Ссылки без текста (только картинка) не годятся как заказ.
	filtered := out[:0]
	for _, o := range out {
		if len([]rune(o.Title)) >= 5 {
			filtered = append(filtered, o)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("на странице не найдено ссылок по шаблону (возможно, нужен вход или сайт изменил вёрстку)")
	}
	return filtered, nil
}

// Details загружает страницу заказа и возвращает его описание и бюджет.
func (f *HTML) Details(ctx context.Context, src store.Source, pageURL string) (string, string, *int, error) {
	body, err := f.get(ctx, src, pageURL)
	if err != nil {
		return "", "", nil, err
	}
	page := string(body)
	desc := ""
	if m := metaDescRe.FindStringSubmatch(page); m != nil {
		desc = cleanText(m[1])
	} else if m := metaRevRe.FindStringSubmatch(page); m != nil {
		desc = cleanText(m[1])
	}
	text := cleanText(scriptRe.ReplaceAllString(page, " "))
	if len([]rune(desc)) < 120 {
		// Мета-описание бывает пустым или обрезанным — добавляем фрагмент текста страницы.
		title := ""
		if m := titleRe.FindStringSubmatch(page); m != nil {
			title = cleanText(m[1])
		}
		frag := text
		if title != "" {
			if i := strings.Index(text, strings.Split(title, " | ")[0]); i >= 0 {
				frag = text[i:]
			}
		}
		desc = strings.TrimSpace(desc + "\n" + truncateRunes(frag, 1500))
	}
	budget, value := extractBudget(text)
	if c, err := parseConfig(src); err == nil && c.DetailsFrom != "" {
		if i := strings.Index(text, c.DetailsFrom); i >= 0 {
			cut := text[i+len(c.DetailsFrom):]
			if j := strings.Index(cut, c.DetailsTo); c.DetailsTo != "" && j >= 0 {
				cut = cut[:j]
			}
			desc = truncateRunes(strings.TrimSpace(cut), 3000)
		}
	}
	return desc, budget, value, nil
}

// cards разбирает страницу, поделённую на карточки заказов.
func (f *HTML) cards(page string, base *url.URL, pattern *regexp.Regexp, c Config) ([]store.RawOrder, error) {
	card, err := regexp.Compile(c.Card)
	if err != nil {
		return nil, fmt.Errorf("card: %w", err)
	}
	re := func(s string) *regexp.Regexp {
		if s == "" {
			return nil
		}
		r, _ := regexp.Compile(s) // уже проверено при сохранении
		return r
	}
	include, titleRe, dateRe, catRe := re(c.Include), re(c.Fields.Title), re(c.Fields.Published), re(c.Fields.Category)
	group := func(r *regexp.Regexp, s string) string {
		if r == nil {
			return ""
		}
		if m := r.FindStringSubmatch(s); len(m) > 1 {
			return cleanText(m[1])
		}
		return ""
	}

	seen := map[string]bool{}
	var out []store.RawOrder
	parts := card.Split(page, -1)
	for _, box := range parts[min(1, len(parts)):] {
		if include != nil && !include.MatchString(box) {
			continue
		}
		link, title := "", ""
		for _, m := range anchorRe.FindAllStringSubmatch(box, -1) {
			if !pattern.MatchString(m[1]) {
				continue
			}
			if link == "" {
				link = m[1]
			}
			if t := cleanText(m[2]); m[1] == link && len([]rune(t)) > len([]rune(title)) && titleRe == nil {
				title = t
			}
		}
		if titleRe != nil {
			title = group(titleRe, box)
		}
		u, err := base.Parse(strings.TrimSpace(link))
		if link == "" || err != nil || len([]rune(title)) < 3 {
			continue
		}
		u.Fragment = ""
		if seen[u.String()] {
			continue
		}
		seen[u.String()] = true
		out = append(out, store.RawOrder{
			ExternalID:  u.String(),
			URL:         u.String(),
			Title:       title,
			Category:    group(catRe, box),
			PublishedAt: parseDate(group(dateRe, box), c.Fields.TimeFormat, c.Fields.Timezone),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("на странице не найдено карточек заказов (проверьте card, include и шаблон ссылок)")
	}
	return out, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
