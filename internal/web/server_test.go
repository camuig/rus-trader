package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
)

// newAuthServer builds a Server with only the fields withBasicAuth touches.
func newAuthServer(user, pass string) *Server {
	return &Server{
		config: &config.Config{
			Web: config.WebConfig{Port: 9000, AuthUser: user, AuthPassword: pass},
		},
		logger: logger.New("error"),
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("dashboard"))
	})
}

func TestWithBasicAuth(t *testing.T) {
	tests := []struct {
		name       string
		user, pass string // configured credentials
		sendCreds  bool
		sendUser   string
		sendPass   string
		wantStatus int
	}{
		{
			name:       "no credentials configured serves everyone",
			sendCreds:  false,
			wantStatus: http.StatusOK,
		},
		{
			name: "correct credentials pass", user: "admin", pass: "s3cret",
			sendCreds: true, sendUser: "admin", sendPass: "s3cret",
			wantStatus: http.StatusOK,
		},
		{
			name: "missing header rejected", user: "admin", pass: "s3cret",
			sendCreds:  false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong password rejected", user: "admin", pass: "s3cret",
			sendCreds: true, sendUser: "admin", sendPass: "wrong",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong user rejected", user: "admin", pass: "s3cret",
			sendCreds: true, sendUser: "root", sendPass: "s3cret",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "empty credentials rejected", user: "admin", pass: "s3cret",
			sendCreds: true, sendUser: "", sendPass: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "password prefix rejected", user: "admin", pass: "s3cret",
			sendCreds: true, sendUser: "admin", sendPass: "s3cre",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAuthServer(tt.user, tt.pass).withBasicAuth(okHandler())

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.sendCreds {
				req.SetBasicAuth(tt.sendUser, tt.sendPass)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized {
				if got := rec.Header().Get("WWW-Authenticate"); got == "" {
					t.Error("401 response is missing the WWW-Authenticate header")
				}
				if rec.Body.String() == "dashboard" {
					t.Error("rejected request still reached the handler")
				}
			}
		})
	}
}

func TestWithBasicAuthGuardsStaticAssets(t *testing.T) {
	h := newAuthServer("admin", "s3cret").withBasicAuth(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("static assets must require auth too: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWebAuthEnabled(t *testing.T) {
	tests := []struct {
		name, user, pass string
		want             bool
	}{
		{"both set", "admin", "s3cret", true},
		{"both empty", "", "", false},
		{"only user", "admin", "", false},
		{"only password", "", "s3cret", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Web: config.WebConfig{AuthUser: tt.user, AuthPassword: tt.pass}}
			if got := cfg.WebAuthEnabled(); got != tt.want {
				t.Errorf("WebAuthEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
