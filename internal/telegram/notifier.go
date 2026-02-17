package telegram

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/camuig/rus-trader/internal/config"
	"github.com/camuig/rus-trader/internal/logger"
)

type Notifier struct {
	bot    *tgbotapi.BotAPI
	chatID int64
	enabled bool
	logger *logger.Logger
}

func NewNotifier(cfg *config.Config, log *logger.Logger) *Notifier {
	if !cfg.Telegram.Enabled {
		return &Notifier{enabled: false, logger: log}
	}

	bot, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		log.Error("failed to create telegram bot", "error", err)
		return &Notifier{enabled: false, logger: log}
	}

	log.Info("telegram bot connected", "username", bot.Self.UserName)

	return &Notifier{
		bot:     bot,
		chatID:  cfg.Telegram.ChatID,
		enabled: true,
		logger:  log,
	}
}

func (n *Notifier) NotifyBuy(ticker string, price float64, lots int64, sl, tp float64) {
	msg := fmt.Sprintf("🟢 *BUY* %s\nЦена: %.2f ₽\nЛоты: %d\nSL: %.2f\nTP: %.2f",
		ticker, price, lots, sl, tp)
	n.send(msg)
}

func (n *Notifier) NotifySell(ticker string, price float64, lots int64, pnl float64) {
	emoji := "🔴"
	if pnl > 0 {
		emoji = "💰"
	}
	msg := fmt.Sprintf("%s *SELL* %s\nЦена: %.2f ₽\nЛоты: %d\nP&L: %.2f ₽",
		emoji, ticker, price, lots, pnl)
	n.send(msg)
}

func (n *Notifier) NotifyError(context string, err error) {
	msg := fmt.Sprintf("⚠️ *Ошибка* [%s]\n%v", context, err)
	n.send(msg)
}

func (n *Notifier) NotifyStatus(message string) {
	n.send(message)
}

func (n *Notifier) send(text string) {
	if !n.enabled {
		return
	}

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown

	if _, err := n.bot.Send(msg); err != nil {
		n.logger.Error("send telegram message", "error", err)
	}
}
