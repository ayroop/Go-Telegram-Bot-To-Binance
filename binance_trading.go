package main

import (
    "encoding/json"
    "fmt"
    "log"
    "strconv"
    "sync"
    "io/ioutil"

    "github.com/adshao/go-binance/v2"
    "github.com/adshao/go-binance/v2/futures"
    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TradingSettings holds the user-configurable trading options.
type TradingSettings struct {
    MarginMode       string  `json:"margin_mode"`        // "cross" or "isolated"
    Leverage         int     `json:"leverage"`           // e.g., 5
    AssetMode        string  `json:"asset_mode"`         // "multi" or "single"
    TradingMode      string  `json:"trading_mode"`       // "limit" or "market"
    AmountUSDT       float64 `json:"amount_usdt"`        // e.g., 100
    SLEnabled        bool    `json:"sl_enabled"`         // Stop Loss enabled or not
	BinanceAPIKey    string  `json:"binance_api_key"`    // User's Binance API Key
	BinanceAPISecret string  `json:"binance_api_secret"` // User's Binance API Secret
}

// DefaultSettings provides the default trading settings.
var DefaultSettings = TradingSettings{
    MarginMode:       "cross",
    Leverage:         5,
    AssetMode:        "multi",
    TradingMode:      "market",
    AmountUSDT:       100,
    SLEnabled:        false,
    BinanceAPIKey:    "",
    BinanceAPISecret: "",
}

// SettingsStore manages the trading settings with concurrency safety.
type SettingsStore struct {
    sync.RWMutex
    settings TradingSettings
}

// NewSettingsStore initializes the settings store with default settings.
func NewSettingsStore() *SettingsStore {
    return &SettingsStore{
        settings: DefaultSettings,
    }
}

// GetSettings returns a copy of the current trading settings.
func (s *SettingsStore) GetSettings() TradingSettings {
    s.RLock()
    defer s.RUnlock()
    return s.settings
}

// UpdateSettings updates the trading settings with the provided values.
func (s *SettingsStore) UpdateSettings(newSettings TradingSettings) {
    s.Lock()
    defer s.Unlock()
    s.settings = newSettings
    // Save to persistent storage if necessary
    if err := saveSettings(s.settings); err != nil {
        log.Printf("Error saving settings: %v", err)
    }
}

// saveSettings persists the settings to a file or database.
// Implement this function based on your storage preference.
func saveSettings(settings TradingSettings) error {
    data, err := json.MarshalIndent(settings, "", "  ")
    if err != nil {
        return err
    }
    // Example: Save to a JSON file
    return writeFile("settings.json", data)
}

// loadSettings loads the settings from a file or database.
// Implement this function based on your storage preference.
func loadSettings() (TradingSettings, error) {
    var settings TradingSettings
    data, err := readFile("settings.json")
    if err != nil {
        return settings, err
    }
    err = json.Unmarshal(data, &settings)
    return settings, err
}

// Initialize the settings store
var tradingSettingsStore = NewSettingsStore()

// BinanceClient manages the Binance API client.
type BinanceClient struct {
    futuresClient *binance.Client
    initialized   bool
    sync.Mutex
}

// NewBinanceClient initializes the Binance client with API credentials.
func NewBinanceClient(apiKey, apiSecret string) *BinanceClient {
    client := binance.NewClient(apiKey, apiSecret)
    client.BaseURL = binance.TestNetFuturesRESTURL // Use Testnet

    return &BinanceClient{
        futuresClient: client,
        initialized:   true,
    }
}

// Global Binance client instance
var binanceClientInstance *BinanceClient
var binanceClientOnce sync.Once

// InitializeBinanceClient initializes the Binance client using stored API credentials.
func InitializeBinanceClient() error {
    tradingSettingsStore.Lock()
    defer tradingSettingsStore.Unlock()

    settings := tradingSettingsStore.settings
    if settings.BinanceAPIKey == "" || settings.BinanceAPISecret == "" {
        return fmt.Errorf("Binance API credentials are not set")
    }

    binanceClientOnce.Do(func() {
        binanceClientInstance = NewBinanceClient(settings.BinanceAPIKey, settings.BinanceAPISecret)
    })

    return nil
}

// Admin Commands to Set Binance API Credentials

// handleSetBinanceAPI handles the /setbinanceapi command to set API Key and Secret.
func handleSetBinanceAPI(message *tgbotapi.Message) {
    chatID := message.Chat.ID

    // Prompt user for API Key
    msg := tgbotapi.NewMessage(chatID, "Please enter your Binance API Key:")
    bot.Send(msg)

    // Set user state to await API Key
    editingUsers.Set(chatID, &EditingState{SignalID: "set_binance_api_key"})
}

// handleBinanceAPIKey handles the user's response for Binance API Key.
func handleBinanceAPIKey(message *tgbotapi.Message) {
    chatID := message.Chat.ID
    apiKey := strings.TrimSpace(message.Text)

    // Prompt user for API Secret
    msg := tgbotapi.NewMessage(chatID, "Please enter your Binance API Secret:")
    bot.Send(msg)

    // Update user state to await API Secret
    editingUsers.Set(chatID, &EditingState{SignalID: "set_binance_api_secret", Field: apiKey})
}

// handleBinanceAPISecret handles the user's response for Binance API Secret.
func handleBinanceAPISecret(message *tgbotapi.Message, apiKey string) {
    chatID := message.Chat.ID
    apiSecret := strings.TrimSpace(message.Text)

    // Update the settings with API credentials
    tradingSettingsStore.Lock()
    tradingSettingsStore.settings.BinanceAPIKey = apiKey
    tradingSettingsStore.settings.BinanceAPISecret = apiSecret
    tradingSettingsStore.Unlock()

    // Save settings
    if err := saveSettings(tradingSettingsStore.settings); err != nil {
        log.Printf("Error saving Binance API credentials: %v", err)
        msg := tgbotapi.NewMessage(chatID, "Failed to save Binance API credentials.")
        bot.Send(msg)
        return
    }

    // Initialize Binance client
    if err := InitializeBinanceClient(); err != nil {
        log.Printf("Error initializing Binance client: %v", err)
        msg := tgbotapi.NewMessage(chatID, "Failed to initialize Binance client. Please check your API credentials.")
        bot.Send(msg)
        return
    }

    msg := tgbotapi.NewMessage(chatID, "Binance API credentials have been set successfully!")
    bot.Send(msg)

    // Clear user editing state
    editingUsers.Delete(chatID)
}

// Setting Commands to Configure Trading Options

// handleSettingsCommand handles the /settings command to display and update trading settings.
func handleSettingsCommand(message *tgbotapi.Message) {
    chatID := message.Chat.ID
    settings := tradingSettingsStore.GetSettings()

    settingsText := fmt.Sprintf(
        "<b>Current Trading Settings:</b>\n\n"+
            "1. <b>Margin Mode:</b> %s\n"+
            "2. <b>Leverage:</b> %dx\n"+
            "3. <b>Asset Mode:</b> %s\n"+
            "4. <b>Trading Mode:</b> %s\n"+
            "5. <b>Amount (USDT):</b> %.2f\n"+
            "6. <b>Stop Loss Enabled:</b> %t\n",
        settings.MarginMode,
        settings.Leverage,
        settings.AssetMode,
        settings.TradingMode,
        settings.AmountUSDT,
        settings.SLEnabled,
    )

    // Inline keyboard for updating settings
    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("1. Margin Mode", "settings|margin_mode"),
            tgbotapi.NewInlineKeyboardButtonData("2. Leverage", "settings|leverage"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("3. Asset Mode", "settings|asset_mode"),
            tgbotapi.NewInlineKeyboardButtonData("4. Trading Mode", "settings|trading_mode"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("5. Amount (USDT)", "settings|amount_usdt"),
            tgbotapi.NewInlineKeyboardButtonData("6. Stop Loss", "settings|stop_loss"),
        ),
    )

    msg := tgbotapi.NewMessage(chatID, settingsText)
    msg.ParseMode = "HTML"
    msg.ReplyMarkup = keyboard

    if _, err := bot.Send(msg); err != nil {
        log.Printf("Failed to send settings message: %v", err)
    }
}

// handleCallbackQuerySettings handles the callback queries for settings updates.
func handleCallbackQuerySettings(callback *tgbotapi.CallbackQuery) {
    data := callback.Data // Expected format: "settings|field_name"
    chatID := callback.Message.Chat.ID
    messageID := callback.Message.MessageID

    parts := strings.Split(data, "|")
    if len(parts) != 2 {
        log.Printf("Invalid settings callback data: %s", data)
        return
    }

    field := parts[1]
    prompt := ""

    switch field {
    case "margin_mode":
        prompt = "Select Margin Mode:\n\n1. cross\n2. isolated"
    case "leverage":
        prompt = "Enter Leverage (e.g., 5):"
    case "asset_mode":
        prompt = "Select Asset Mode:\n\n1. multi\n2. single"
    case "trading_mode":
        prompt = "Select Trading Mode:\n\n1. limit\n2. market"
    case "amount_usdt":
        prompt = "Enter Amount in USDT (e.g., 100):"
    case "stop_loss":
        prompt = "Enable Stop Loss? (yes/no):"
    default:
        log.Printf("Unknown settings field: %s", field)
        return
    }

    // Prompt user for the new value
    msg := tgbotapi.NewMessage(chatID, prompt)
    bot.Send(msg)

    // Update user editing state
    editingUsers.Set(chatID, &EditingState{SignalID: "update_setting", Field: field})
}

// handleNewSettingsValue handles the new value provided by the user for a setting.
func handleNewSettingsValue(message *tgbotapi.Message, editingState *EditingState) {
    chatID := message.Chat.ID
    newValue := strings.TrimSpace(message.Text)
    field := editingState.Field

    tradingSettingsStore.Lock()
    defer tradingSettingsStore.Unlock()

    settings := tradingSettingsStore.settings

    switch field {
    case "margin_mode":
        if newValue != "cross" && newValue != "isolated" {
            msg := tgbotapi.NewMessage(chatID, "Invalid Margin Mode. Please enter 'cross' or 'isolated'.")
            bot.Send(msg)
            return
        }
        settings.MarginMode = newValue
    case "leverage":
        leverage, err := strconv.Atoi(newValue)
        if err != nil || leverage < 1 || leverage > 125 {
            msg := tgbotapi.NewMessage(chatID, "Invalid Leverage. Please enter a number between 1 and 125.")
            bot.Send(msg)
            return
        }
        settings.Leverage = leverage
    case "asset_mode":
        if newValue != "multi" && newValue != "single" {
            msg := tgbotapi.NewMessage(chatID, "Invalid Asset Mode. Please enter 'multi' or 'single'.")
            bot.Send(msg)
            return
        }
        settings.AssetMode = newValue
    case "trading_mode":
        if newValue != "limit" && newValue != "market" {
            msg := tgbotapi.NewMessage(chatID, "Invalid Trading Mode. Please enter 'limit' or 'market'.")
            bot.Send(msg)
            return
        }
        settings.TradingMode = newValue
    case "amount_usdt":
        amount, err := strconv.ParseFloat(newValue, 64)
        if err != nil || amount <= 0 {
            msg := tgbotapi.NewMessage(chatID, "Invalid Amount. Please enter a positive number.")
            bot.Send(msg)
            return
        }
        settings.AmountUSDT = amount
    case "stop_loss":
        if strings.ToLower(newValue) == "yes" {
            settings.SLEnabled = true
        } else if strings.ToLower(newValue) == "no" {
            settings.SLEnabled = false
        } else {
            msg := tgbotapi.NewMessage(chatID, "Invalid input. Please enter 'yes' or 'no'.")
            bot.Send(msg)
            return
        }
    default:
        msg := tgbotapi.NewMessage(chatID, "Unknown setting field.")
        bot.Send(msg)
        return
    }

    // Save updated settings
    tradingSettingsStore.settings = settings
    if err := saveSettings(settings); err != nil {
        log.Printf("Error saving updated settings: %v", err)
        msg := tgbotapi.NewMessage(chatID, "Failed to save settings.")
        bot.Send(msg)
        return
    }

    // Acknowledge the update
    msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Setting '%s' updated successfully.", field))
    bot.Send(msg)

    // Clear user editing state
    editingUsers.Delete(chatID)
}

// confirmSignal marks a signal as confirmed, updates the message, and places a trade on Binance.
func confirmSignal(chatID int64, messageID int, signalID string, callback *tgbotapi.CallbackQuery) {
    log.Printf("confirmSignal called with chatID: %d, messageID: %d, signalID: %s", chatID, messageID, signalID)

    signal, exists := signalStore.Get(signalID)
    if !exists {
        msg := tgbotapi.NewMessage(chatID, "Signal not found.")
        bot.Send(msg)
        return
    }

    // Mark as confirmed
    signal.Confirmed = true
    signalStore.Set(signalID, signal)

    // Update the Telegram message to reflect confirmation
    confirmationText := constructSignalMessageText(signal)
    edit := tgbotapi.NewEditMessageText(chatID, messageID, confirmationText)
    edit.ParseMode = "HTML"
    edit.ReplyMarkup = nil

    if _, err := bot.Send(edit); err != nil {
        log.Printf("Failed to edit message: %v", err)
    }

    // Place the trade on Binance
    go executeTrade(signal)
}

// executeTrade places an order on Binance based on the confirmed signal and user settings.
func executeTrade(signal *AlertMessage) {
    settings := tradingSettingsStore.GetSettings()

    // Initialize Binance client if not already initialized
    binanceClientInstance.Lock()
    if !binanceClientInstance.initialized {
        err := InitializeBinanceClient()
        if err != nil {
            log.Printf("Binance client initialization failed: %v", err)
            return
        }
    }
    binanceClientInstance.Unlock()

    client := binanceClientInstance.futuresClient

    // Set margin type
    _, err := client.NewFuturesChangeMarginTypeService().
        Symbol(signal.Symbol).
        MarginType(settings.MarginMode).
        Do(newDefaultContext())
    if err != nil {
        log.Printf("Failed to set margin type: %v", err)
        return
    }

    // Set leverage
    _, err = client.NewFuturesChangeLeverageService().
        Symbol(signal.Symbol).
        Leverage(settings.Leverage).
        Do(newDefaultContext())
    if err != nil {
        log.Printf("Failed to set leverage: %v", err)
        return
    }

    // Prepare order parameters
    side := binance.SideTypeSell
    if strings.ToLower(signal.Signal) == "buy" {
        side = binance.SideTypeBuy
    }

    orderType := binance.OrderTypeMarket
    if settings.TradingMode == "limit" {
        orderType = binance.OrderTypeLimit
    }

    quantity, err := usdtToQuantity(signal.Symbol, settings.AmountUSDT, client)
    if err != nil {
        log.Printf("Failed to calculate quantity: %v", err)
        return
    }

    orderService := client.NewFuturesCreateOrderService().
        Symbol(signal.Symbol).
        Side(side).
        Type(orderType).
        Quantity(strconv.FormatFloat(quantity, 'f', -1, 64))

    if orderType == binance.OrderTypeLimit {
        price := strconv.FormatFloat(signal.EntryPrice, 'f', -1, 64)
        orderService = orderService.Price(price).TimeInForce(binance.TimeInForceTypeGTC)
    }

    // Place the order
    order, err := orderService.Do(newDefaultContext())
    if err != nil {
        log.Printf("Failed to place order: %v", err)
        return
    }

    log.Printf("Order placed successfully: %+v", order)

    // Place Stop Loss if enabled
    if settings.SLEnabled && signal.SL != 0 {
        stopSide := binance.SideTypeBuy
        if strings.ToLower(signal.Signal) == "buy" {
            stopSide = binance.SideTypeSell
        }

        stopPrice := strconv.FormatFloat(signal.SL, 'f', -1, 64)
        stopOrderType := binance.OrderTypeStop

        _, err := client.NewFuturesCreateOrderService().
            Symbol(signal.Symbol).
            Side(stopSide).
            Type(stopOrderType).
            Quantity(strconv.FormatFloat(quantity, 'f', -1, 64)).
            StopPrice(stopPrice).
            ClosePosition(false).
            Do(newDefaultContext())
        if err != nil {
            log.Printf("Failed to place Stop Loss order: %v", err)
        } else {
            log.Printf("Stop Loss order placed at price: %s", stopPrice)
        }
    }

    // Place Take Profit orders TP1 to TP4
    takeProfitLevels := []float64{signal.TP1, signal.TP2, signal.TP3, signal.TP4}
    for i, tp := range takeProfitLevels {
        if tp == 0 {
            continue
        }

        tpSide := binance.SideTypeSell
        if strings.ToLower(signal.Signal) == "buy" {
            tpSide = binance.SideTypeBuy
        }

        tpPrice := strconv.FormatFloat(tp, 'f', -1, 64)
        tpOrderType := binance.OrderTypeTakeProfit

        _, err := client.NewFuturesCreateOrderService().
            Symbol(signal.Symbol).
            Side(tpSide).
            Type(tpOrderType).
            Quantity(strconv.FormatFloat(quantity/4, 'f', -1, 64)). // Distribute equally
            Price(tpPrice).
            ClosePosition(false).
            TimeInForce(binance.TimeInForceTypeGTC).
            Do(newDefaultContext())
        if err != nil {
            log.Printf("Failed to place TP%d order: %v", i+1, err)
        } else {
            log.Printf("Take Profit TP%d order placed at price: %s", i+1, tpPrice)
        }
    }
}


// usdtToQuantity converts USDT amount to quantity based on symbol's price.
func usdtToQuantity(symbol string, usdtAmount float64, client *binance.Client) (float64, error) {
    // Get the latest price for the symbol
    klines, err := client.NewFuturesKlinesService().
        Symbol(symbol).
        Interval("1m").
        Limit(1).
        Do(newDefaultContext())
    if err != nil {
        return 0, err
    }

    if len(klines) == 0 {
        return 0, fmt.Errorf("no kline data received")
    }

    lastPrice, err := strconv.ParseFloat(klines[0].Close, 64)
    if err != nil {
        return 0, err
    }

    quantity := usdtAmount / lastPrice
    return quantity, nil
}

// newDefaultContext returns a default context for API calls.
// Implement context with timeout if necessary.
func newDefaultContext() *futures.Context {
    return &futures.Context{}
}

// Utility functions for reading and writing files.
// Implement these based on your application's requirements.

// writeFile writes data to a file.
func writeFile(filename string, data []byte) error {
    return ioutil.WriteFile(filename, data, 0644)
}

// readFile reads data from a file.
func readFile(filename string) ([]byte, error) {
    return ioutil.ReadFile(filename)
}

// Update handleMessage to process settings updates and Binance API credentials.

func handleMessage(message *tgbotapi.Message) {
    chatID := message.Chat.ID

    editingState, editing := editingUsers.Get(chatID)

    if editing && editingState.SignalID == "set_binance_api_key" {
        handleBinanceAPIKey(message)
        return
    }

    if editing && editingState.SignalID == "set_binance_api_secret" && editingState.Field != "" {
        handleBinanceAPISecret(message, editingState.Field)
        return
    }

    if editing && editingState.SignalID == "update_setting" && editingState.Field != "" {
        handleNewSettingsValue(message, editingState)
        return
    }

    // Existing message handling logic...
    // ...
    
    // Example: Handle /setbinanceapi and /settings commands
    if message.IsCommand() {
        switch message.Command() {
        case "setbinanceapi":
            handleSetBinanceAPI(message)
        case "settings":
            handleSettingsCommand(message)
        // Add other command handlers as needed
        default:
            handleCommand(message)
        }
    } else {
        // Existing non-command message handling
        // ...
    }
}

// Extend handleCallbackQuery to manage settings callbacks.

func handleCallbackQuery(callback *tgbotapi.CallbackQuery) {
    data := callback.Data
    chatID := callback.Message.Chat.ID
    messageID := callback.Message.MessageID

    if data == "" {
        return
    }

    // Split the callback data
    parts := strings.Split(data, "|")
    if len(parts) < 2 {
        log.Printf("Invalid callback data: '%s'", data)
        return
    }

    action := parts[0]
    payload := parts[1]
    var fieldName string
    if len(parts) > 2 {
        fieldName = parts[2]
    }

    switch action {
    case ActionEdit:
        showEditOptions(chatID, messageID, payload)
    case ActionField:
        if fieldName == "" {
            log.Printf("Field name missing in callback data: '%s'", data)
            return
        }
        promptNewFieldValue(chatID, payload, fieldName)
    case ActionConfirm:
        confirmSignal(chatID, messageID, payload, callback)
    case "settings":
        handleCallbackQuerySettings(callback)
    default:
        log.Printf("Unknown callback action: '%s'", action)
    }

    // Acknowledge the callback
    callbackConfig := tgbotapi.NewCallback(callback.ID, "")
    if _, err := bot.Request(callbackConfig); err != nil {
        log.Printf("Callback acknowledgement failed: %v", err)
    }
}

// Ensure to load settings on bot startup.

func init() {
    settings, err := loadSettings()
    if err != nil {
        log.Println("No existing settings found. Using default settings.")
        tradingSettingsStore.UpdateSettings(DefaultSettings)
    } else {
        tradingSettingsStore.UpdateSettings(settings)
    }

    if settings.BinanceAPIKey != "" && settings.BinanceAPISecret != "" {
        if err := InitializeBinanceClient(); err != nil {
            log.Printf("Failed to initialize Binance client: %v", err)
        } else {
            log.Println("Binance client initialized successfully.")
        }
    }
}

// Update sendSignalMessage to include SL information if enabled.

func sendSignalMessage(alert AlertMessage) (int, error) {
    if bot == nil {
        return 0, fmt.Errorf("Telegram bot is not initialized")
    }
    if GlobalConfig.TelegramChatID == 0 {
        return 0, fmt.Errorf("Telegram Chat ID is not set")
    }

    signalID := sanitizeSignalID(alert.SignalID)
    if signalID == "" {
        log.Printf("signalID is empty after sanitization")
        signalID = fmt.Sprintf("signal_%d", time.Now().UnixNano())
        log.Printf("Assigned new signalID: '%s'", signalID)
    }

    // Store the signal
    signalStore.Set(signalID, alert)

    telegramMessage := constructSignalMessageText(&alert)

    msg := tgbotapi.NewMessage(GlobalConfig.TelegramChatID, telegramMessage)
    msg.ParseMode = "HTML"

    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("Edit", fmt.Sprintf("%s|%s", ActionEdit, signalID)),
            tgbotapi.NewInlineKeyboardButtonData("Confirm", fmt.Sprintf("%s|%s", ActionConfirm, signalID)),
        ),
    )
    msg.ReplyMarkup = keyboard

    message, err := bot.Send(msg)
    if err != nil {
        return 0, fmt.Errorf("failed to send message to Telegram: %w", err)
    }

    // Store the message ID
    messageStore.Set(signalID, message.MessageID)

    return message.MessageID, nil
}