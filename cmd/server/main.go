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

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
)

func main() {
	cfg := config.LoadConfig()

	logger.SetOutput(os.Stdout, logger.ParseLevel(cfg.LogLevel), cfg.LogFormat)
	srvLog := logger.WithComponent("server")
	srvLog.Info("initializing ex-chat application", "env", cfg.Environment, "log_level", cfg.LogLevel, "log_format", cfg.LogFormat)

	db, err := database.InitDB(cfg)
	if err != nil {
		srvLog.Error("database initialization fatal error", "error", err.Error())
		log.Fatalf("database initialization error: %v", err)
	}

	hub := ws.GetHub()

	engine := router.SetupRouter(cfg, db, hub)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      engine,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		srvLog.Info("server listening", "port", cfg.Port, "env", cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvLog.Error("server listen fatal error", "error", err.Error())
			log.Fatalf("server listen error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	srvLog.Info("shutting down server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		srvLog.Error("server forced to shutdown", "error", err.Error())
		log.Fatalf("server forced to shutdown: %v", err)
	}

	srvLog.Info("server exited cleanly")
}
