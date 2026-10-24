package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr      string
	DatabaseURL   string
	MLURL         string
	AdminUsername string
	AdminPassword string
	SecretKey     string
	PublicURL     string
	CookieSecure  bool
	TelegramToken string
	// Значения по умолчанию для настроек, которые потом можно менять в веб-интерфейсе.
	NotifyMaxAge time.Duration
	RetrainEvery int
	UserAgent    string
	FetchTimeout time.Duration
	// JSON-файл с готовыми площадками для кнопки «Готовые площадки» (у каждой установки свой).
	PresetsFile string
}

func Load() Config {
	return Config{
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		DatabaseURL:   env("DATABASE_URL", "postgres://sobachka:sobachka@localhost:5432/sobachka?sslmode=disable"),
		MLURL:         env("ML_URL", "http://localhost:8000"),
		AdminUsername: env("ADMIN_USERNAME", "admin"),
		AdminPassword: env("ADMIN_PASSWORD", ""),
		SecretKey:     env("APP_SECRET_KEY", ""),
		PublicURL:     env("PUBLIC_URL", "https://localhost"),
		CookieSecure:  env("COOKIE_SECURE", "true") != "false",
		TelegramToken: env("TELEGRAM_BOT_TOKEN", ""),
		NotifyMaxAge:  time.Duration(envInt("NOTIFY_MAX_AGE_MIN", 360)) * time.Minute,
		RetrainEvery:  envInt("RETRAIN_EVERY", 10),
		UserAgent:     env("FETCH_USER_AGENT", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36"),
		FetchTimeout:  time.Duration(envInt("FETCH_TIMEOUT_SEC", 40)) * time.Second,
		PresetsFile:   env("PRESETS_FILE", "config/presets.json"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func (c Config) Validate() error {
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("PUBLIC_URL должен быть origin без пути, например https://example.com")
	}
	if c.CookieSecure && u.Scheme != "https" {
		return errors.New("COOKIE_SECURE=true требует HTTPS в PUBLIC_URL")
	}
	if !c.CookieSecure && u.Scheme == "https" {
		return errors.New("для HTTPS установите COOKIE_SECURE=true")
	}
	return nil
}
