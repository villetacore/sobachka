package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sobachka/internal/api"
	"sobachka/internal/config"
	"sobachka/internal/core"
	"sobachka/internal/ml"
	"sobachka/internal/security"
	"sobachka/internal/store"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("БД: %v", err)
	}
	defer st.Close()
	if err := st.Seed(ctx); err != nil {
		log.Fatalf("начальные данные: %v", err)
	}
	if _, err := security.Encrypt(cfg.SecretKey, "startup-check"); err != nil {
		log.Fatal(err)
	}
	if err := st.Bootstrap(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Fatalf("первый администратор: %v", err)
	}
	mlc := ml.New(cfg.MLURL)
	svc := core.New(cfg, st, mlc, nil)
	if err := svc.InitTelegram(ctx); err != nil {
		log.Fatalf("Telegram: %v", err)
	}
	go svc.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.New(cfg, st, svc, mlc).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("API слушает %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
