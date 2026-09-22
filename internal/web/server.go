package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"time"

	"github.com/camuig/rus-trader/internal/broker"
	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
	"github.com/camuig/rus-trader/internal/moex"
	"github.com/camuig/rus-trader/internal/storage"
)

type Server struct {
	httpServer *http.Server
	broker     *broker.BrokerClient
	repo       *storage.Repository
	config     *config.Config
	logger     *logger.Logger
	moex       *moex.Client
}

func NewServer(bc *broker.BrokerClient, repo *storage.Repository, cfg *config.Config, log *logger.Logger, mc *moex.Client) *Server {
	s := &Server{
		broker: bc,
		repo:   repo,
		config: cfg,
		logger: log,
		moex:   mc,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleDashboard)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Web.Port),
		Handler:      s.withBasicAuth(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return s
}

// withBasicAuth guards the dashboard when web.auth_user/web.auth_password are set.
// Without credentials configured it is a no-op, so local loopback runs stay unchanged.
func (s *Server) withBasicAuth(next http.Handler) http.Handler {
	if !s.config.WebAuthEnabled() {
		return next
	}

	// Hash both sides so the comparison leaks neither length nor content via timing.
	wantUser := sha256.Sum256([]byte(s.config.Web.AuthUser))
	wantPass := sha256.Sum256([]byte(s.config.Web.AuthPassword))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if ok {
			gotUser := sha256.Sum256([]byte(user))
			gotPass := sha256.Sum256([]byte(pass))
			userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) == 1
			passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) == 1
			if userOK && passOK {
				next.ServeHTTP(w, r)
				return
			}
		}

		s.logger.Info("dashboard auth rejected", "remote", r.RemoteAddr, "path", r.URL.Path)
		w.Header().Set("WWW-Authenticate", `Basic realm="rus-trader", charset="UTF-8"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func (s *Server) Start() error {
	s.logger.Info("web server starting", "port", s.config.Web.Port, "auth", s.config.WebAuthEnabled())
	if !s.config.WebAuthEnabled() {
		s.logger.Info("dashboard has no authentication (web.auth_user/web.auth_password are empty)")
	}
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("web server: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
