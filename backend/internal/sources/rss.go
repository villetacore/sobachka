package sources

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sobachka/internal/store"
)

// RSS разбирает RSS 2.0 и Atom ленты.
type RSS struct {
	client *http.Client
	ua     string
}

type rssDoc struct {
	Items   []rssItem   `xml:"channel>item"`
	Entries []atomEntry `xml:"entry"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        string   `xml:"guid"`
	Description string   `xml:"description"`
	Encoded     string   `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Categories  []string `xml:"category"`
	PubDate     string   `xml:"pubDate"`
}

type atomEntry struct {
	ID      string `xml:"id"`
	Title   string `xml:"title"`
	Summary string `xml:"summary"`
	Content string `xml:"content"`
	Updated string `xml:"updated"`
	Links   []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"link"`
	Categories []struct {
		Term string `xml:"term,attr"`
	} `xml:"category"`
}

func (f *RSS) Fetch(ctx context.Context, src store.Source) ([]store.RawOrder, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.ua)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")
	if src.Cookie != "" {
		req.Header.Set("Cookie", src.Cookie)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	var doc rssDoc
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	// Ленты в не-UTF-8 кодировках встречаются редко; читаем как есть, чтобы декодер не падал.
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("разбор ленты: %w", err)
	}

	var out []store.RawOrder
	for _, it := range doc.Items {
		// WordPress кладёт в description короткую выжимку, а полный текст — в content:encoded.
		desc := it.Description
		if len(it.Encoded) > len(desc) {
			desc = it.Encoded
		}
		out = append(out, makeOrder(it.GUID, it.Link, it.Title, desc, strings.Join(it.Categories, " / "), parseTime(it.PubDate)))
	}
	for _, e := range doc.Entries {
		link := ""
		for _, l := range e.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				link = l.Href
				break
			}
		}
		desc := e.Summary
		if desc == "" {
			desc = e.Content
		}
		var cats []string
		for _, c := range e.Categories {
			cats = append(cats, c.Term)
		}
		out = append(out, makeOrder(e.ID, link, e.Title, desc, strings.Join(cats, " / "), parseTime(e.Updated)))
	}
	if len(out) == 0 && looksLikeHTML(body) {
		// Нестрогий XML-декодер «съедает» HTML без ошибки — без этой проверки источник молча ничего не находит.
		return nil, fmt.Errorf("по адресу не RSS-лента, а обычная страница: выберите тип «HTML-страница» или готовую площадку")
	}
	return out, nil
}

func looksLikeHTML(body []byte) bool {
	head := body
	if len(head) > 1024 {
		head = head[:1024]
	}
	h := strings.ToLower(string(head))
	return strings.Contains(h, "<!doctype html") || strings.Contains(h, "<html")
}

func makeOrder(guid, link, title, desc, category string, published *time.Time) store.RawOrder {
	title = cleanText(title)
	desc = cleanText(desc)
	id := strings.TrimSpace(guid)
	if id == "" {
		id = strings.TrimSpace(link)
	}
	if id == "" {
		id = title
	}
	budget, value := extractBudget(title, desc)
	return store.RawOrder{
		ExternalID:  id,
		URL:         stripTracking(strings.TrimSpace(link)),
		Title:       title,
		Description: desc,
		Category:    cleanText(category),
		Budget:      budget,
		BudgetValue: value,
		PublishedAt: published,
	}
}

// stripTracking убирает utm-метки из ссылки.
func stripTracking(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.RawQuery == "" {
		return link
	}
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(k, "utm_") {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func parseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
