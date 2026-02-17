package broker

import (
	"context"
	"fmt"

	"github.com/russianinvestments/invest-api-go-sdk/investgo"

	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
)

const (
	sandboxEndpoint = "sandbox-invest-public-api.tinkoff.ru:443"
	liveEndpoint    = "invest-public-api.tinkoff.ru:443"
)

type BrokerClient struct {
	Client *investgo.Client
	Config *config.Config
	Logger *logger.Logger
}

func NewBrokerClient(ctx context.Context, cfg *config.Config, log *logger.Logger) (*BrokerClient, error) {
	endpoint := liveEndpoint
	if cfg.IsSandbox() {
		endpoint = sandboxEndpoint
	}

	investCfg := investgo.Config{
		EndPoint:  endpoint,
		Token:     cfg.Tinkoff.Token,
		AccountId: cfg.Tinkoff.AccountID,
		AppName:   "rus-trader",
	}

	client, err := investgo.NewClient(ctx, investCfg, log)
	if err != nil {
		return nil, fmt.Errorf("create investgo client: %w", err)
	}

	bc := &BrokerClient{
		Client: client,
		Config: cfg,
		Logger: log,
	}

	if cfg.IsSandbox() && cfg.Tinkoff.AccountID == "" {
		if err := bc.setupSandbox(); err != nil {
			return nil, fmt.Errorf("setup sandbox: %w", err)
		}
	}

	return bc, nil
}

func (bc *BrokerClient) setupSandbox() error {
	sandbox := bc.Client.NewSandboxServiceClient()

	// Top up sandbox account with 1,000,000 RUB
	_, err := sandbox.SandboxPayIn(&investgo.SandboxPayInRequest{
		AccountId: bc.Client.Config.AccountId,
		Currency:  "RUB",
		Unit:      1000000,
		Nano:      0,
	})
	if err != nil {
		return fmt.Errorf("sandbox pay in: %w", err)
	}

	bc.Logger.Info("sandbox account funded", "account_id", bc.Client.Config.AccountId)
	return nil
}

func (bc *BrokerClient) AccountID() string {
	return bc.Client.Config.AccountId
}

func (bc *BrokerClient) Stop() error {
	return bc.Client.Stop()
}
