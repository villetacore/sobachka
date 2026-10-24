// Package ml — HTTP-клиент к Python-сервису с нейросетью оценки заказов.
package ml

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	base string
	hc   *http.Client
}

func New(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), hc: &http.Client{Timeout: 2 * time.Minute}}
}

type Order struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

type Profile struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Keywords []string `json:"keywords"`
}

type Score struct {
	ProfileID int64    `json:"profile_id"`
	Score     float64  `json:"score"`
	Base      float64  `json:"base"`
	Head      *float64 `json:"head"`
	Keywords  []string `json:"matched_keywords"`
}

type Health struct {
	Status string         `json:"status"`
	Head   map[string]any `json:"head"`
}

type TrainResult struct {
	Samples  int     `json:"samples"`
	Positive int     `json:"positive"`
	Negative int     `json:"negative"`
	Loss     float64 `json:"loss"`
	Accuracy float64 `json:"accuracy"`
	ValSize  int     `json:"val_size"`
	// Сколько примеров — ваши 👍/👎, а сколько сеть разметила сама по ключевым словам.
	UserSamples int `json:"user_samples"`
	AutoSamples int `json:"auto_samples"`
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Detail any `json:"detail"`
		}
		if json.Unmarshal(data, &e) == nil && e.Detail != nil {
			return fmt.Errorf("ml %s: %v", path, e.Detail)
		}
		return fmt.Errorf("ml %s: HTTP %d", path, resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.do(ctx, http.MethodGet, "/health", nil, &h)
	return h, err
}

func (c *Client) Score(ctx context.Context, o Order, profiles []Profile) ([]Score, error) {
	var out struct {
		Scores []Score `json:"scores"`
	}
	err := c.do(ctx, http.MethodPost, "/score", map[string]any{"order": o, "profiles": profiles}, &out)
	return out.Scores, err
}

// Train передаёт оценки пользователя, свежие заказы для самообучения и актуальные профили.
func (c *Client) Train(ctx context.Context, examples any, unlabeled []Order, profiles []Profile) (TrainResult, error) {
	var out TrainResult
	err := c.do(ctx, http.MethodPost, "/train", map[string]any{"examples": examples, "unlabeled": unlabeled, "profiles": profiles}, &out)
	return out, err
}
