# Rus-Trader

Автоматизированный торговый бот для российского фондового рынка (MOEX). Собирает рыночные данные (топ-50 ликвидных акций TQBR, часовые свечи, новости), анализирует их через DeepSeek R1 и автоматически исполняет сделки через T-Invest API.

## Архитектура

```
MOEX ISS API → топ-50 тикеров по объёму (TQBR)
      ↓
T-Invest SDK → резолв тикеров в UID, проверка tradability
      ↓
T-Invest SDK → часовые свечи за неделю (параллельно)
      ↓
MOEX ISS API → новости за 24ч → фильтр по тикерам
      ↓
T-Invest SDK → текущий портфель
      ↓
Indicators   → RSI(14), EMA(9/21), ATR(14), RelVol, S/R уровни
      ↓
Screener     → фильтрация тикеров по силе сигнала
      ↓
DeepSeek R1  → анализ индикаторов + OHLCV + новостей → JSON решения
      ↓
TradeGuard   → pre-validation (RSI > 80, время суток, лимиты)
      ↓
Executor     → лимитные ордера + SL/TP + trailing stop
      ↓
SQLite + Telegram + Web Dashboard
```

**Стек:** Go, T-Invest API (gRPC), MOEX ISS API, DeepSeek R1, SQLite (GORM), Telegram Bot API

## Быстрый старт

### Требования

- Go 1.22+
- Токен T-Invest API ([получить](https://www.tbank.ru/invest/settings/))
- API ключ DeepSeek ([получить](https://platform.deepseek.com/))

### Настройка

```bash
cp config.example.yaml config.yaml
# Отредактируйте config.yaml — укажите токены
```

### Запуск

```bash
# Собрать всё
make build

# Запустить бота
make run

# Docker
make docker
```

Dashboard доступен на `http://localhost:8080`

## Makefile

| Команда           | Описание                                  |
|-------------------|-------------------------------------------|
| `make build`      | Собрать бинарники `bot` и `closeall`      |
| `make run`        | Собрать и запустить бота                  |
| `make close-all`  | Закрыть все открытые позиции              |
| `make close-all-dry` | Показать позиции без закрытия (dry run)|
| `make docker`     | Запустить в Docker                        |
| `make docker-down`| Остановить Docker                         |
| `make clean`      | Удалить артефакты сборки                  |

## Утилиты

### Закрытие всех позиций

Скрипт `cmd/closeall` закрывает все текущие позиции рыночными ордерами:

```bash
# Показать позиции (без закрытия)
make close-all-dry

# Закрыть все позиции
make close-all

# Или напрямую
go run ./cmd/closeall/ -config config.yaml -dry-run
go run ./cmd/closeall/ -config config.yaml
```

## Параметры конфигурации

| Параметр | Описание | По умолчанию |
|----------|----------|--------------|
| `tinkoff.token` | API токен T-Invest | (обязательный) |
| `tinkoff.sandbox` | Режим песочницы | `true` |
| `tinkoff.account_id` | ID аккаунта (авто в sandbox) | `""` |
| `deepseek.api_key` | API ключ DeepSeek | (обязательный) |
| `deepseek.model` | Модель DeepSeek | `deepseek-reasoner` |
| `deepseek.timeout_seconds` | Таймаут запроса | `120` |
| `trading.interval` | Интервал анализа | `15m` |
| `trading.max_position_rub` | Макс. на позицию (руб) | `10000` |
| `trading.min_confidence` | Мин. уверенность AI (0-100) | `70` |
| `trading.default_stop_loss_pct` | Stop-Loss % | `3.0` |
| `trading.default_take_profit_pct` | Take-Profit % | `5.0` |
| `trading.candle_concurrency` | Параллелизм загрузки свечей | `10` |
| `trading.max_spread_pct` | Макс. спред bid/ask для BUY (%) | `0.3` |
| `trading.trailing_stop_enabled` | Включить trailing stop | `false` |
| `trading.trailing_breakeven_pct` | % к TP для переноса SL на безубыток | `50` |
| `trading.trailing_lock_profit_pct` | % к TP для фиксации 50% прибыли | `75` |
| `trading.limit_order_slippage` | Отступ для лимитных ордеров (%), 0=market | `0.1` |
| `trading.no_last_hour_buy` | Запрет BUY в последний час торгов | `false` |
| `telegram.enabled` | Включить уведомления | `false` |
| `telegram.bot_token` | Токен Telegram бота | |
| `telegram.chat_id` | Chat ID для уведомлений | |
| `web.port` | Порт веб-дашборда | `8080` |

## Telegram

1. Создайте бота через [@BotFather](https://t.me/BotFather)
2. Узнайте свой Chat ID через [@userinfobot](https://t.me/userinfobot)
3. Укажите `bot_token` и `chat_id` в `config.yaml`
4. Установите `telegram.enabled: true`

## Торговые улучшения

### Технические индикаторы
Бот автоматически рассчитывает RSI(14), EMA(9), EMA(21), ATR(14), относительный объём и уровни поддержки/сопротивления для каждого тикера. Индикаторы передаются в AI для более точного анализа.

### Предварительный скрининг
Перед отправкой в AI тикеры ранжируются по силе технического сигнала (RSI-экстремумы, EMA-кроссоверы, аномальные объёмы). В анализ попадают только самые перспективные кандидаты.

### Trailing Stop
При включении (`trailing_stop_enabled: true`) бот автоматически подтягивает SL:
- При достижении 50% пути к TP — SL переносится на безубыток
- При достижении 75% пути к TP — SL фиксирует 50% прибыли

### Масштабирование позиций
Размер позиции зависит от уверенности AI: confidence 90+ = 100%, 80-89 = 75%, 70-79 = 50% от `max_position_rub`.

### Лимитные ордера
При `limit_order_slippage > 0` вместо рыночных ордеров используются лимитные с указанным отступом от текущей цены, что снижает проскальзывание.

### Фильтр по спреду
Перед покупкой проверяется bid/ask спред. Тикеры со спредом выше `max_spread_pct` пропускаются.

### Pre-validation решений
TradeGuard механически блокирует BUY при RSI > 80 (перекупленность) и в последний час торгов (17:50-18:50 MSK).

### Статистика в промпте
AI получает агрегированную статистику за 7 дней: win rate, средний профит/убыток, худшие тикеры — для более осознанных решений.

## Торговые часы

Бот активен в торговые часы MOEX: **10:00–18:50 MSK**, понедельник–пятница.

## Режим песочницы

По умолчанию бот работает в sandbox-режиме. Аккаунт создаётся автоматически и пополняется на 1,000,000 руб. Stop-ордера в sandbox не поддерживаются T-Invest API.

Для перехода на реальную торговлю установите `tinkoff.sandbox: false` и укажите `tinkoff.account_id`.
