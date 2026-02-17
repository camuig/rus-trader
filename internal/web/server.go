package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/storage"
)

type Server struct {
	httpServer *http.Server
	broker     *broker.BrokerClient
	repo       *storage.Repository
	config     *config.Config
	logger     *logger.Logger
}

func NewServer(bc *broker.BrokerClient, repo *storage.Repository, cfg *config.Config, log *logger.Logger) *Server {
	s := &Server{
		broker: bc,
		repo:   repo,
		config: cfg,
		logger: log,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleDashboard)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Web.Port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return s
}

func (s *Server) Start() error {
	s.logger.Info("web server starting", "port", s.config.Web.Port)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("web server: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
