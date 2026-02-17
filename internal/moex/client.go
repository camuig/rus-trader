package moex

import (
	"net/http"
	"time"

	"github.com/camuig/rus-trader/internal/logger"
)

type Client struct {
	httpClient *http.Client
	logger     *logger.Logger
}

func NewClient(log *logger.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     log,
	}
}
