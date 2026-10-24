package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sobachka/internal/store"
)

func TestExtractBudget(t *testing.T) {
	cases := map[string]int{
		"Сделать сайт (Бюджет: 15 000 &#8381;)": 15000,
		"бюджет 500$":            500,
		"Бюджет: до 3000 руб.":   3000,
		"Сайт-визитка - 4000UAH": 4000,
	}
	for in, want := range cases {
		_, got := extractBudget(cleanText(in))
		if got == nil || *got != want {
			t.Errorf("%q: got %v, want %d", in, got, want)
		}
	}
	if _, v := extractBudget("без бюджета"); v != nil {
		t.Errorf("unexpected budget %d", *v)
	}
}

// Живая проверка готовых площадок из файла установки:
// PRESETS_FILE=$PWD/../config/presets.json LIVE=1 go test ./internal/sources -run Live -v
func TestLivePresets(t *testing.T) {
	if os.Getenv("LIVE") == "" {
		t.Skip("set LIVE=1")
	}
	presets, err := LoadPresets(os.Getenv("PRESETS_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) == 0 {
		t.Skip("PRESETS_FILE не задан или пуст")
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	for _, p := range presets {
		src := store.Source{Kind: p.Kind, URL: p.URL, Pattern: p.Pattern, Config: p.Config}
		f, _ := New(src.Kind, hc, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36")
		items, err := f.Fetch(context.Background(), src)
		if err != nil || len(items) == 0 {
			t.Errorf("%s: %v (items=%d)", p.Name, err, len(items))
			continue
		}
		it := items[0]
		if d, ok := f.(Detailer); ok && it.Description == "" {
			desc, budget, _, err := d.Details(context.Background(), src, it.URL)
			if err != nil {
				t.Errorf("details %s: %v", it.URL, err)
			}
			it.Description, it.Budget = desc, budget
		}
		t.Logf("%s: %d items; first: %q | %s | budget=%q | %v | desc=%.200q", p.Name, len(items), it.Title, it.URL, it.Budget, it.PublishedAt, it.Description)
	}
}

func TestRSSRejectsHTMLPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<!DOCTYPE html><html><body><a href=\"/projects/1/x.html\">Заказ</a></body></html>"))
	}))
	defer srv.Close()
	f, _ := New("rss", srv.Client(), "test")
	if _, err := f.Fetch(context.Background(), store.Source{Kind: "rss", URL: srv.URL}); err == nil {
		t.Fatal("ожидалась ошибка для HTML-страницы в RSS-источнике")
	}
}

func TestLoadPresets(t *testing.T) {
	if ps, err := LoadPresets(filepath.Join(t.TempDir(), "нет.json")); err != nil || len(ps) != 0 {
		t.Fatalf("нет файла — пустой список без ошибки: %v %v", ps, err)
	}
	path := filepath.Join(t.TempDir(), "presets.json")
	_ = os.WriteFile(path, []byte(`[{"name":"Пример","kind":"json","url":"https://example.com/api","config":{"items":"list"}}]`), 0o600)
	ps, err := LoadPresets(path)
	if err != nil || len(ps) != 1 || string(ps[0].Config) != `{"items":"list"}` {
		t.Fatalf("got %+v, %v", ps, err)
	}
	_ = os.WriteFile(path, []byte(`[{"name":"Плохой","kind":"unknown","url":"https://example.com"}]`), 0o600)
	if _, err := LoadPresets(path); err == nil {
		t.Fatal("ожидалась ошибка для неизвестного типа")
	}
	// Пример из репозитория должен оставаться корректным.
	ps, err = LoadPresets("../../../config/presets.example.json")
	if err != nil || len(ps) == 0 {
		t.Fatalf("presets.example.json: %v (%d)", err, len(ps))
	}
	for _, p := range ps {
		if err := ValidateConfig(p.Kind, p.Config); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}
	}
}

func TestJSONSource(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Method != http.MethodPost || r.Header.Get("X-Test") != "1" || r.PostForm.Get("cat") != "all" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		pages = append(pages, r.PostForm.Get("page"))
		if r.PostForm.Get("page") == "1" {
			_, _ = w.Write([]byte(`{"data":{"list":[
				{"id":7,"name":"Сделать <b>сайт</b>","text":"Лендинг","price":{"max":5000},"state":"open","created":"2026-10-24 10:00:00","tags":["web","html"]},
				{"id":8,"name":"Закрытый","state":"closed"},
				{"id":9,"name":"Без бюджета","state":"open","created":"2026-10-24T10:00:00Z"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"list":[]}}`))
	}))
	defer srv.Close()
	src := store.Source{Kind: "json", URL: srv.URL + "/api", Config: []byte(`{
		"method":"POST","form":{"cat":"all"},"headers":{"X-Test":"1"},"page_param":"page","pages":3,
		"items":"data.items|data.list","filter":{"state":"open"},
		"fields":{"id":"id","title":"name","description":"text","url":"/task/{id}","category":"{tags}",
			"budget_max":"price.max","published":"created","timezone":"+03:00"}}`)}
	if err := ValidateConfig(src.Kind, src.Config); err != nil {
		t.Fatal(err)
	}
	f, _ := New("json", srv.Client(), "test")
	items, err := f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pages, ",") != "1,2" {
		t.Errorf("страницы: %v (после пустой страницы читать дальше не нужно)", pages)
	}
	if len(items) != 2 {
		t.Fatalf("ожидалось 2 открытых заказа, получено %+v", items)
	}
	it := items[0]
	if it.Title != "Сделать сайт" || it.URL != srv.URL+"/task/7" || it.ExternalID != "7" || it.Category != "web, html" ||
		it.Budget != "до 5000 ₽" || *it.BudgetValue != 5000 || it.PublishedAt == nil || it.PublishedAt.UTC().Hour() != 7 {
		t.Errorf("unexpected item: %+v", it)
	}
	if items[1].Budget != "" || items[1].PublishedAt == nil {
		t.Errorf("unexpected item: %+v", items[1])
	}
}

func TestHTMLCards(t *testing.T) {
	page := `<div class="card"><span class="date">от 21.10.2026</span>
		<a href="/item/230/"><img src="x.png"></a><h3><a href="/item/230/">Нужна вёрстка</a></h3>
		<a href="/item/230/">Длинное описание, которое не должно стать заголовком</a>
		<span class="type">Заказ</span></div>
	<div class="card"><h3><a href="/item/229/">Предлагаю услуги</a></h3><span class="type">Реклама</span></div>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(page)) }))
	defer srv.Close()
	src := store.Source{Kind: "html", URL: srv.URL + "/list/", Pattern: `/item/\d+/`, Config: []byte(`{
		"card":"<div class=\"card\">","include":"class=\"type\">Заказ",
		"fields":{"title":"<h3><a[^>]*>(.*?)</a>","published":"от (\\d{2}\\.\\d{2}\\.\\d{4})","time_format":"02.01.2006",
			"category":"class=\"type\">([^<]*)"}}`)}
	if err := ValidateConfig(src.Kind, src.Config); err != nil {
		t.Fatal(err)
	}
	f, _ := New("html", srv.Client(), "test")
	items, err := f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Нужна вёрстка" || items[0].URL != srv.URL+"/item/230/" ||
		items[0].Category != "Заказ" || items[0].PublishedAt == nil || items[0].PublishedAt.Day() != 21 {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestHTMLDetailsBetweenMarkers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><meta name="description" content="Общий текст сайта"></head>
			<body><p>Меню</p><p>Описание заказа</p><p>Сделать лендинг. Бюджет: 7000 ₽</p><p>Вложения</p><p>Подвал</p></body></html>`))
	}))
	defer srv.Close()
	src := store.Source{Kind: "html", Config: []byte(`{"details_from":"Описание заказа","details_to":"Вложения"}`)}
	desc, budget, _, err := (&HTML{client: srv.Client(), ua: "test"}).Details(context.Background(), src, srv.URL)
	if err != nil || desc != "Сделать лендинг. Бюджет: 7000 ₽" || budget != "7000 ₽" {
		t.Fatalf("desc=%q budget=%q err=%v", desc, budget, err)
	}
}
