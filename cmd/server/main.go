package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"limesurvey_redirector/internal/auth"
	"limesurvey_redirector/internal/config"
	"limesurvey_redirector/internal/credentials"
	"limesurvey_redirector/internal/limesurvey"
	"limesurvey_redirector/internal/store"
	"limesurvey_redirector/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		log.Fatal(err)
	}

	instanceSecrets, err := credentials.NewProtector(cfg.InstanceCredentialsKey)
	if err != nil {
		log.Fatal(err)
	}
	authManager := auth.New(cfg.AdminUsername, cfg.AdminPassword, cfg.SessionSecret, cfg.SecureCookies)
	lsService := limesurvey.NewService(cfg.StatsTTL, cfg.RequestTimeout, instanceSecrets)
	server, err := web.NewServer(cfg, st, authManager, lsService, instanceSecrets)
	if err != nil {
		log.Fatal(err)
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-shutdownCtx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	log.Printf("listening on %s", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
