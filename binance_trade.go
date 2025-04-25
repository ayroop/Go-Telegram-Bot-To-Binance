package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"runtime/debug"
	"strconv"
	"sync"

	"github.com/adshao/go-binance/v2/futures"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/gorilla/websocket"
)

const (
	MarginTypeCross    futures.MarginType = "CROSSED"
	MarginTypeIsolated futures.MarginType = "ISOLATED"
)

type BinanceClient struct {
	Client *futures.Client
	Bot    *tgbotapi.BotAPI
	mu     sync.Mutex // Mutex for concurrency control
}

// safeGo runs the given function in a new goroutine and logs panics.
// Use for launching goroutines that interact with external APIs or clients.
func (b *BinanceClient) safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PANIC][%s]: %v\nStack trace: %s", name, r, debug.Stack())
				if b.Bot != nil && b != nil {
					// Try to notify admin about the panic
					config := GetGlobalConfig()
					if config.AdminUserID != 0 {
						b.sendMessageToUser(config.AdminUserID, fmt.Sprintf("⚠️ Panic in goroutine [%s]: %v", name, r))
					}
				}
			}
		}()
		fn()
}

func NewBinanceClient(botInstance *tgbotapi.BotAPI) *BinanceClient {
	config := GetGlobalConfig()
	client := futures.NewClient(config.BinanceAPIKey, config.BinanceAPISecret)
	client.BaseURL = config.BinanceAPIURL
	client.Debug = true

	binanceClient := &BinanceClient{
		Client: client,
		Bot:    botInstance,
	}

	if err := binanceClient.testAPIKey(); err != nil {
		log.Fatalf("Binance API key test failed: %v", err)
	} else {
		log.Println("Binance API key is valid and has required permissions.")
	}

	return binanceClient
}

// Validate Binance API
func validateBinanceAPIKeys(apiKey, apiSecret, apiURL string) error {
	client := futures.NewClient(apiKey, apiSecret)
	client.BaseURL = apiURL
	_, err := client.NewGetAccountService().Do(context.Background())
	if err != nil {
		return fmt.Errorf("invalid Binance API Key/Secret: %v", err)
	}
	log.Printf("[ExecuteTrade] User %d | Symbol: %s | Complete", userID, signal.Symbol)
	return nil
}
func (b *BinanceClient) testAPIKey() error {
	_, err := b.Client.NewGetAccountService().Do(context.Background())
	if err != nil {
		return fmt.Errorf("API key test failed: %v", err)
	}
	return nil
}

// ExecuteTrade places an order (Market or Limit) and then places TPs/SL as needed.
func (b *BinanceClient) ExecuteTrade(signal *AlertMessage, settings *UserSettings, userID int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	log.Printf("[ExecuteTrade] User %d | Starting execution | Symbol: %s | Signal: %#v | Settings: %#v", 
		userID, signal.Symbol, signal, settings)

	if signal == nil {
		err := fmt.Errorf("no valid signal provided")
		b.sendMessageToUser(userID, "Signal not found or invalid.")
		log.Printf("[ExecuteTrade] User %d | Failed: signal is nil", userID)
		return err
	}

	symbol := signal.Symbol
	if symbol == "" {
		err := fmt.Errorf("signal has an empty symbol field")
		b.sendMessageToUser(userID, "Signal has no symbol specified.")
		return err
	}

	side := futures.SideTypeBuy
	if signal.SignalType == "Sell" {
		side = futures.SideTypeSell
	}

	// Recalculate SL and TP based on settings
	if settings.AutoCalculateTPs {
		recalcSingleTPAndSL(signal, settings)
	} else {
		recalcManualTPAndSL(signal, settings)
	}

	// Set margin mode + leverage (e.g., Cross/Isolated, 5x)
	if err := b.setMarginModeAndLeverage(symbol, settings); err != nil {
		return fmt.Errorf("failed to set margin mode or leverage: %v", err)
	}

	// Calculate quantity from the user's USDT amount and the signal's entry price
	quantity, err := b.calculateQuantity(symbol, settings.AmountUSDT, signal.EntryPrice)
	if err != nil {
		msg := fmt.Sprintf("Failed to calculate quantity for %s: %v", symbol, err)
		b.sendMessageToUser(userID, msg)
		return err
	}

	// Place Market or Limit order
	if settings.TradingMode == "Market" {
		err = b.placeMarketOrder(symbol, side, quantity)
		if err != nil {
			txt := fmt.Sprintf("Failed to execute trade for %s: %v", symbol, err)
			b.sendMessageToUser(userID, txt)
			return err
		}
		txt := fmt.Sprintf("Trade executed for %s (%s) at market price", symbol, settings.TradingMode)
		b.sendMessageToUser(userID, txt)

		// If TP/SL is relevant, place OCO orders
		if (signal.TP1 != 0) || (settings.UseSL && signal.SL > 0) {
			err = b.placeOCOOrder(symbol, side, quantity, signal, settings)
			if err != nil {
				msg := fmt.Sprintf("Failed to place TPs/SL for %s: %v", symbol, err)
				b.sendMessageToUser(userID, msg)
				return err
			}
			msg := fmt.Sprintf("TP/SL orders placed for %s.", symbol)
			b.sendMessageToUser(userID, msg)

			// Start monitoring the orders
			b.safeGo("monitorOrdersViaWebSocket", func() {
				b.monitorOrdersViaWebSocket(userID)
			})
		}
	} else if settings.TradingMode == "Limit" {
		err = b.placeLimitOrder(symbol, side, quantity, signal.EntryPrice)
		if err != nil {
			txt := fmt.Sprintf("Failed to execute trade for %s: %v", symbol, err)
			b.sendMessageToUser(userID, txt)
			return err
		}
		txt := fmt.Sprintf("Trade executed for %s (%s) at price %.4f", symbol, settings.TradingMode, signal.EntryPrice)
		b.sendMessageToUser(userID, txt)
	}

	return nil
}

// monitorOrdersViaWebSocket uses WebSocket to monitor order status and sends a notification if they are hit.
func (b *BinanceClient) monitorOrdersViaWebSocket(userID int64) {
	// Start user data stream to get a listen key
	listenKey, err := b.Client.NewStartUserStreamService().Do(context.Background())
	if err != nil {
		log.Fatalf("Failed to start user stream: %v", err)
	}

	wsURL := "wss://fstream.binance.com/ws/" + listenKey // Append listenKey to the WebSocket URL
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer conn.Close()

	// Listen for messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Error reading WebSocket message: %v", err)
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal(message, &event); err != nil {
			log.Printf("Error unmarshalling WebSocket message: %v", err)
			continue
		}

		// Check for order execution event
		if eventType, ok := event["e"].(string); ok && eventType == "ORDER_TRADE_UPDATE" {
			order := event["o"].(map[string]interface{})
			orderStatus := order["X"].(string)
			if orderStatus == "FILLED" {
				symbol := order["s"].(string)
				clientOrderID := order["c"].(string)
				msg := fmt.Sprintf("Order %s for %s has been filled.", clientOrderID, symbol)
				b.sendMessageToUser(userID, msg)
			}
		}
	}
}

// recalcSingleTPAndSL calculates a single TP (TP1) and SL for the signal based on user settings.
func recalcSingleTPAndSL(signal *AlertMessage, settings *UserSettings) {
	entry := signal.EntryPrice
	if entry <= 0 {
		return
	}

	tpPct := settings.AutoTPPercentage / 100.0
	slPct := settings.AutoSLPercentage / 100.0

	// Zero out additional TPs since we only use TP1 if auto-calc is toggled
	signal.TP2, signal.TP3 = 0, 0

	if signal.SignalType == "Sell" {
		signal.TP1 = entry * (1 - tpPct)
		if settings.UseSL {
			signal.SL = entry * (1 + slPct)
		} else {
			signal.SL = 0
		}
	} else {
		signal.TP1 = entry * (1 + tpPct)
		if settings.UseSL {
			signal.SL = entry * (1 - slPct)
		} else {
			signal.SL = 0
		}
	}
}

// recalcManualTPAndSL calculates TPs and SL based on manual settings.
func recalcManualTPAndSL(signal *AlertMessage, settings *UserSettings) {
	entry := signal.EntryPrice
	if entry <= 0 {
		return
	}

	tp1Pct := settings.TP1Percentage / 100.0
	tp2Pct := settings.TP2Percentage / 100.0
	tp3Pct := settings.TP3Percentage / 100.0
	slPct := settings.ManualSLPercentage / 100.0

	if signal.SignalType == "Sell" {
		signal.TP1 = entry * (1 - tp1Pct)
		signal.TP2 = entry * (1 - tp2Pct)
		signal.TP3 = entry * (1 - tp3Pct)
		if settings.UseSL {
			signal.SL = entry * (1 + slPct)
		} else {
			signal.SL = 0
		}
	} else {
		signal.TP1 = entry * (1 + tp1Pct)
		signal.TP2 = entry * (1 + tp2Pct)
		signal.TP3 = entry * (1 + tp3Pct)
		if settings.UseSL {
			signal.SL = entry * (1 - slPct)
		} else {
			signal.SL = 0
		}
	}
}

// setMarginModeAndLeverage configures the margin mode and leverage on Binance Futures.
func (b *BinanceClient) setMarginModeAndLeverage(symbol string, settings *UserSettings) error {
	var marginType futures.MarginType
	if settings.MarginMode == "Isolated" {
		marginType = MarginTypeIsolated
	} else {
		marginType = MarginTypeCross
	}

	// First set the margin type
	err := b.Client.NewChangeMarginTypeService().
		Symbol(symbol).
		MarginType(marginType).
		Do(context.Background())
	if err != nil {
		log.Printf("Failed to set margin mode for %s: %v", symbol, err)
	}

	// Then set leverage
	leverage := settings.Leverage
	if leverage <= 0 {
		leverage = 5
	}
	_, err = b.Client.NewChangeLeverageService().
		Symbol(symbol).
		Leverage(leverage).
		Do(context.Background())
	if err != nil {
		return fmt.Errorf("failed to set leverage: %v", err)
	}
	return nil
}

// calculateQuantity computes an order quantity based on the user's USDT amount and the entry price.
func (b *BinanceClient) calculateQuantity(symbol string, amountUSDT, entryPrice float64) (string, error) {
	sInfo, err := b.getSymbolInfo(symbol)
	if err != nil {
		return "", err
	}
	if entryPrice <= 0 {
		return "", fmt.Errorf("entry price is invalid (<= 0)")
	}

	// Basic formula: quantity = USDT / price
	quantity := amountUSDT / entryPrice

	// Retrieve the "LOT_SIZE" stepSize from the symbol info
	stepStr, err := getFilterValue(sInfo.Filters, "LOT_SIZE", "stepSize")
	if err != nil {
		return "", fmt.Errorf("failed to get step size: %v", err)
	}
	stepSize, err := strconv.ParseFloat(stepStr, 64)
	if err != nil {
		return "", fmt.Errorf("failed to parse step size: %v", err)
	}

	// Round down based on the step size
	quantity = math.Floor(quantity/stepSize) * stepSize
	return formatDecimal(quantity, stepSize), nil
}

// getSymbolInfo fetches symbol details (filters, etc.) from Binance exchange info.
func (b *BinanceClient) getSymbolInfo(symbol string) (*futures.Symbol, error) {
	info, err := b.Client.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		return nil, err
	}
	for _, s := range info.Symbols {
		if s.Symbol == symbol {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("symbol %s not found", symbol)
}

// getCurrentPrice retrieves the current price for the given symbol.
func (b *BinanceClient) getCurrentPrice(symbol string) (float64, error) {
	stats, err := b.Client.NewListPriceChangeStatsService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return 0, err
	}
	if len(stats) == 0 {
		return 0, fmt.Errorf("no price data for symbol %s", symbol)
	}
	cp, err := strconv.ParseFloat(stats[0].LastPrice, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse current price: %v", err)
	}
	return cp, nil
}

// placeMarketOrder submits a Market order to Binance Futures.
func (b *BinanceClient) placeMarketOrder(symbol string, side futures.SideType, quantity string) error {
	_, err := b.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		Type(futures.OrderTypeMarket).
		Quantity(quantity).
		Do(context.Background())
	return err
}

// placeLimitOrder submits a Limit (GTC) order to Binance Futures at user's specified price.
func (b *BinanceClient) placeLimitOrder(symbol string, side futures.SideType, quantity string, price float64) error {
	info, err := b.getSymbolInfo(symbol)
	if err != nil {
		return err
	}
	pStr, err := b.formatPrice(info, price)
	if err != nil {
		return err
	}

	_, err = b.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		Type(futures.OrderTypeLimit).
		TimeInForce(futures.TimeInForceTypeGTC).
		Quantity(quantity).
		Price(pStr).
		Do(context.Background())

	return err
}

// placeOCOOrder places the relevant Take-Profit and Stop-Loss orders.
func (b *BinanceClient) placeOCOOrder(symbol string, side futures.SideType, quantity string, signal *AlertMessage, settings *UserSettings) error {
	tpSide := invertSide(side)
	slSide := invertSide(side)

	// If auto-calc was used, only TP1 is relevant
	if settings.AutoCalculateTPs {
		if signal.TP1 > 0 {
			if err := b.placeTPOrder(symbol, tpSide, quantity, signal.TP1); err != nil {
				return err
			}
		}
	} else {
		// Otherwise place up to three TPs
		tps := []float64{signal.TP1, signal.TP2, signal.TP3}
		for _, tpPrice := range tps {
			if tpPrice <= 0 {
				continue
			}
			if err := b.placeTPOrder(symbol, tpSide, quantity, tpPrice); err != nil {
				return err
			}
		}
	}

	if settings.UseSL && signal.SL > 0 {
		if err := b.placeSLOrder(symbol, slSide, quantity, signal.SL); err != nil {
			return err
		}
	}
	return nil
}

// placeTPOrder places a Take-Profit-Market order for a given TP price.
func (b *BinanceClient) placeTPOrder(symbol string, side futures.SideType, quantity string, tpPrice float64) error {
	_, err := b.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		Type(futures.OrderTypeTakeProfitMarket).
		StopPrice(fmt.Sprintf("%.4f", tpPrice)).
		ClosePosition(true).
		WorkingType(futures.WorkingTypeMarkPrice).
		PriceProtect(true).
		Do(context.Background())

	return err
}

// placeSLOrder places a Stop-Loss-Market order at the given price.
func (b *BinanceClient) placeSLOrder(symbol string, side futures.SideType, quantity string, slPrice float64) error {
	_, err := b.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		Type(futures.OrderTypeStopMarket).
		StopPrice(fmt.Sprintf("%.4f", slPrice)).
		ClosePosition(true).
		WorkingType(futures.WorkingTypeMarkPrice).
		PriceProtect(true).
		Do(context.Background())
	return err
}

// formatPrice rounds the price based on the symbol's PRICE_FILTER tickSize.
func (b *BinanceClient) formatPrice(symbolInfo *futures.Symbol, price float64) (string, error) {
	tsStr, err := getFilterValue(symbolInfo.Filters, "PRICE_FILTER", "tickSize")
	if err != nil {
		return "", fmt.Errorf("failed to get tick size: %v", err)
	}
	tickSize, err := strconv.ParseFloat(tsStr, 64)
	if err != nil {
		return "", fmt.Errorf("failed to parse tick size: %v", err)
	}

	price = math.Round(price/tickSize) * tickSize
	return formatDecimal(price, tickSize), nil
}

// getFilterValue extracts a string value by filterType and paramName from the Filters array.
func getFilterValue(filters []map[string]interface{}, filterType, paramName string) (string, error) {
	for _, fMap := range filters {
		ft, ok := fMap["filterType"].(string)
		if !ok {
			continue
		}
		if ft == filterType {
			v, ok := fMap[paramName].(string)
			if !ok {
				return "", fmt.Errorf("%s not found in %s filter", paramName, filterType)
			}
			return v, nil
		}
	}
	return "", fmt.Errorf("%s filter not found", filterType)
}

// formatDecimal constructs a string with the correct decimal places (derived from step size).
func formatDecimal(value, step float64) string {
	decimalPlaces := 0
	for step < 1 {
		step *= 10
		decimalPlaces++
	}
	return fmt.Sprintf("%."+strconv.Itoa(decimalPlaces)+"f", value)
}

// invertSide flips Buy order to Sell (for TPs) or vice versa.
func invertSide(side futures.SideType) futures.SideType {
	if side == futures.SideTypeBuy {
		return futures.SideTypeSell
	}
	return futures.SideTypeBuy
}

// sendMessageToUser sends a text message to the specified Telegram userID.
func (b *BinanceClient) sendMessageToUser(userID int64, message string) {
	msg := tgbotapi.NewMessage(userID, message)
	if _, err := b.Bot.Send(msg); err != nil {
		log.Printf("Failed to send message to user %d: %v", userID, err)
	}
}
