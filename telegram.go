package main

import (
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Global variables
var binanceClient *BinanceClient
var bot *tgbotapi.BotAPI

// Synchronization primitives
var (
	editingUsers     = NewEditingUsers()
	signalStore      = NewSignalStore()
	messageStore     = NewMessageStore()
	userSettings     = NewUserSettingsStore()
	updatesChan      tgbotapi.UpdatesChannel
	listenerShutdown chan struct{}
)

// Constants for action types
const (
	ActionEdit         = "edit"
	ActionField        = "field"
	ActionConfirm      = "conf"
	ActionDismiss      = "dismiss"
	ActionSettings     = "settings"
	ActionSetOption    = "setopt"
	ActionChangeOption = "chgopt"
	ActionPerformance  = "performance"
)

// EditingState represents the state of a user editing a signal or settings.
type EditingState struct {
	SignalID    string
	Field       string
	SettingName string
}

// UserSettings represents a user's settings for trading options.
type UserSettings struct {
	MarginMode                  string  // Cross or Isolated
	Leverage                    int     // e.g., 5x
	AssetMode                   string  // Multi or Single
	TradingMode                 string  // Limit or Market
	AmountUSDT                  float64 // Trading amount in USDT
	UseSL                       bool    // Whether to use Stop Loss
	AutoCalculateTPs            bool    // Whether to auto-calculate TPs/SL
	TP1Percentage               float64 // +1% from entry for TP1 (example)
	TP2Percentage               float64 // +3% from entry for TP2 (example)
	TP3Percentage               float64 // +7% from entry for TP3 (example)
	ManualSLPercentage          float64 // SL percentage for manual calculation
	AutoSLPercentage            float64 // SL percentage for auto calculation
	AutoTPPercentage            float64 // (Not strictly needed if we use TP1-3, but kept if you want a single "TP1" only calc)
	MarketPriceTolerance        float64 // Tolerance for market price difference
	TP1ClosePct                 float64
	TP2ClosePct                 float64
	TP3ClosePct                 float64
	TP1Enabled                  bool
	TP2Enabled                  bool
	TP3Enabled                  bool
	DynamicCalculationEnabled   bool // New field to enable/disable dynamic calculation
	EnableToleranceInMarketMode bool // New field to enable/disable tolerance in Market mode
}

// UserSettingsStore manages user settings with concurrency safety.
type UserSettingsStore struct {
	sync.RWMutex
	settings map[int64]*UserSettings
}

// NewUserSettingsStore creates a new instance of UserSettingsStore.
func NewUserSettingsStore() *UserSettingsStore {
	return &UserSettingsStore{
		settings: make(map[int64]*UserSettings),
	}
}

// Validate Telegram API
func validateTelegramAPIKey(botToken string) error {
	_, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return fmt.Errorf("invalid Telegram Bot Token: %v", err)
	}
	return nil
}

// Get retrieves user settings, creating default settings if none exist
func (s *UserSettingsStore) Get(userID int64) *UserSettings {
	s.RLock()
	defer s.RUnlock()

	settings, exists := s.settings[userID]
	if !exists {
		// Create default settings
		settings = &UserSettings{
			MarginMode:                  "Cross",
			Leverage:                    5,
			AssetMode:                   "Multi",
			TradingMode:                 "Market",
			AmountUSDT:                  100,
			UseSL:                       false,
			AutoCalculateTPs:            false,
			TP1Percentage:               0.75,
			TP2Percentage:               1.5,
			TP3Percentage:               2.0,
			ManualSLPercentage:          1.0,
			AutoSLPercentage:            1.0,
			AutoTPPercentage:            1.0,
			MarketPriceTolerance:        0, // Set to 0 for Market mode
			TP1ClosePct:                 60.0,
			TP2ClosePct:                 20.0,
			TP3ClosePct:                 20.0,
			TP1Enabled:                  true,
			TP2Enabled:                  true,
			TP3Enabled:                  true,
			DynamicCalculationEnabled:   true,
			EnableToleranceInMarketMode: true, // Default to true
		}

		// Initialize TP visibility based on close percentages
		adjustTPClosePercentages(settings)

		// Store the settings in the map
		s.RUnlock()
		s.Lock()
		s.settings[userID] = settings
		s.Unlock()
		s.RLock()
	}

	return settings
}

// Set stores user settings with proper locking and validation
func (s *UserSettingsStore) Set(userID int64, settings *UserSettings) {
	s.Lock()
	defer s.Unlock()

	// Ensure Market Price Tolerance is 0 for Market mode
	if settings.TradingMode == "Market" {
		settings.MarketPriceTolerance = 0
	}

	// Validate and adjust TP percentages
	if settings.TP1ClosePct > 100 {
		settings.TP1ClosePct = 100
	}
	if settings.TP2ClosePct > 100 {
		settings.TP2ClosePct = 100
	}
	if settings.TP3ClosePct > 100 {
		settings.TP3ClosePct = 100
	}

	// Calculate total percentage
	total := settings.TP1ClosePct + settings.TP2ClosePct + settings.TP3ClosePct

	// If total exceeds 100%, adjust proportionally
	if total > 100 {
		ratio := 100 / total
		settings.TP1ClosePct *= ratio
		settings.TP2ClosePct *= ratio
		settings.TP3ClosePct *= ratio
	}

	// Reset all TPs to visible first
	settings.TP1Enabled = true // TP1 is always enabled
	settings.TP2Enabled = true
	settings.TP3Enabled = true

	// If TP1 is 100% or more, disable other TPs
	if settings.TP1ClosePct >= 100 {
		settings.TP1ClosePct = 100
		settings.TP2ClosePct = 0
		settings.TP3ClosePct = 0
		settings.TP2Enabled = false
		settings.TP3Enabled = false
	} else {
		// Calculate remaining percentage after TP1
		remainingPct := 100 - settings.TP1ClosePct

		// Handle TP2
		if settings.TP2ClosePct > remainingPct {
			settings.TP2ClosePct = remainingPct
			settings.TP3ClosePct = 0
			settings.TP3Enabled = false
		} else {
			// Calculate remaining percentage after TP2
			remainingPct -= settings.TP2ClosePct

			// Handle TP3
			if remainingPct <= 0 {
				settings.TP3ClosePct = 0
				settings.TP3Enabled = false
			} else {
				settings.TP3ClosePct = remainingPct
				settings.TP3Enabled = true
			}
		}
	}

	// Update TP enabled states based on final percentages
	settings.TP2Enabled = settings.TP2ClosePct > 0
	settings.TP3Enabled = settings.TP3ClosePct > 0

	// Store the validated settings
	s.settings[userID] = settings

	// Log the update
	log.Printf("Updated settings for user %d: Mode=%s, TPs=[%.2f, %.2f, %.2f], Enabled=[%v, %v, %v]",
		userID,
		settings.TradingMode,
		settings.TP1ClosePct,
		settings.TP2ClosePct,
		settings.TP3ClosePct,
		settings.TP1Enabled,
		settings.TP2Enabled,
		settings.TP3Enabled)
}

// AlertMessage represents a trading signal or alert.
type AlertMessage struct {
	SignalID          string  `json:"signal_id"`
	SignalType        string  `json:"signal"` // "Buy" or "Sell"
	Symbol            string  `json:"symbol"`
	Timeframe         string  `json:"timeframe"`
	Time              string  `json:"time"`
	EntryPrice        float64 `json:"entry_price"`
	TP1               float64 `json:"tp1"`
	TP2               float64 `json:"tp2"`
	TP3               float64 `json:"tp3"`
	SL                float64 `json:"sl"`
	HighPrice         float64 `json:"high_price"`
	LowPrice          float64 `json:"low_price"`
	Midpoint          float64 `json:"midpoint"`
	Confirmed         bool    `json:"confirmed"`
	Dismissed         bool    `json:"dismissed"`
	ManualEntryEdited bool    `json:"manual_entry_edited"`
}

// SignalStore manages signals with concurrency safety.
type SignalStore struct {
	sync.RWMutex
	signals map[string]*AlertMessage
}

// NewSignalStore creates a new instance of SignalStore.
func NewSignalStore() *SignalStore {
	return &SignalStore{
		signals: make(map[string]*AlertMessage),
	}
}

func (s *SignalStore) Set(signalID string, alert *AlertMessage) {
	s.Lock()
	defer s.Unlock()
	s.signals[signalID] = alert
}

func (s *SignalStore) Get(signalID string) (*AlertMessage, bool) {
	s.RLock()
	defer s.RUnlock()
	alert, exists := s.signals[signalID]
	return alert, exists
}

func (s *SignalStore) GetLatestUnconfirmedSignals(limit int) []*AlertMessage {
	s.RLock()
	defer s.RUnlock()

	var unconfirmedSignals []*AlertMessage
	for _, signal := range s.signals {
		if !signal.Confirmed && !signal.Dismissed {
			unconfirmedSignals = append(unconfirmedSignals, signal)
		}
	}

	// Sort signals by some criteria if needed, e.g., by time
	// sort.Slice(unconfirmedSignals, func(i, j int) bool {
	//     timeI, _ := time.Parse(time.RFC3339, unconfirmedSignals[i].Time)
	//     timeJ, _ := time.Parse(time.RFC3339, unconfirmedSignals[j].Time)
	//     return timeI.After(timeJ)
	// })

	if len(unconfirmedSignals) > limit {
		return unconfirmedSignals[:limit]
	}
	return unconfirmedSignals
}

// EditingUsers manages users' editing states with concurrency safety.
type EditingUsers struct {
	sync.RWMutex
	users map[int64]*EditingState
}

// NewEditingUsers creates a new instance of EditingUsers.
func NewEditingUsers() *EditingUsers {
	return &EditingUsers{
		users: make(map[int64]*EditingState),
	}
}

func (e *EditingUsers) Set(userID int64, state *EditingState) {
	e.Lock()
	defer e.Unlock()
	e.users[userID] = state
}

func (e *EditingUsers) Get(userID int64) (*EditingState, bool) {
	e.RLock()
	defer e.RUnlock()
	state, exists := e.users[userID]
	return state, exists
}

func (e *EditingUsers) Delete(userID int64) {
	e.Lock()
	defer e.Unlock()
	delete(e.users, userID)
}

// MessageStore stores message IDs associated with signals.
type MessageStore struct {
	sync.RWMutex
	messages map[string]int
}

// NewMessageStore creates a new instance of MessageStore.
func NewMessageStore() *MessageStore {
	return &MessageStore{
		messages: make(map[string]int),
	}
}

func (m *MessageStore) Set(signalID string, messageID int) {
	m.Lock()
	defer m.Unlock()
	m.messages[signalID] = messageID
}

func (m *MessageStore) Get(signalID string) (int, bool) {
	m.RLock()
	defer m.RUnlock()
	msgID, exists := m.messages[signalID]
	return msgID, exists
}

// PerformanceData represents the performance data structure
type PerformanceData struct {
	TotalTrades   int
	WinningTrades int
	LosingTrades  int
	WinLossRatio  float64
	AverageProfit float64
	AverageLoss   float64
	TotalProfit   float64
	TotalLoss     float64
	NetProfit     float64
}

// initTelegramBot initializes the Telegram bot.
func initTelegramBot(config *Config) (*tgbotapi.BotAPI, error) {
	if bot != nil {
		log.Println("Re-initializing Telegram bot with new configuration.")
		stopTelegramListener()
		// Recreate the BinanceClient with the existing bot
		binanceClient = NewBinanceClient(bot)
		bot = nil
	}

	if config.TelegramBotToken == "" {
		return nil, fmt.Errorf("Telegram Bot Token is not set")
	}

	var err error
	bot, err = tgbotapi.NewBotAPI(config.TelegramBotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Telegram bot: %v", err)
	}
	bot.Debug = false
	log.Printf("Authorized on account %s", bot.Self.UserName)

	binanceClient = NewBinanceClient(bot)
	startTelegramListener()

	return bot, nil
}

// startTelegramListener starts the listener for Telegram updates.
func startTelegramListener() {
	if updatesChan != nil {
		return
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updatesChan = bot.GetUpdatesChan(u)
	listenerShutdown = make(chan struct{})

	go func() {
		defer close(listenerShutdown)
		for update := range updatesChan {
			if update.CallbackQuery != nil {
				handleCallbackQuery(update.CallbackQuery)
			} else if update.Message != nil {
				handleMessage(update.Message)
			}
		}
	}()
}

// stopTelegramListener stops the Telegram updates listener.
func stopTelegramListener() {
	if updatesChan != nil {
		bot.StopReceivingUpdates()
		<-listenerShutdown
		updatesChan = nil
		listenerShutdown = nil
	}
}

// handleMessage processes incoming messages.
func handleMessage(message *tgbotapi.Message) {
	chatID := message.Chat.ID
	editingState, editing := editingUsers.Get(chatID)

	if editing {
		// If user is currently editing a signal field or setting
		if editingState.Field != "" {
			handleNewFieldValue(message, editingState)
			editingUsers.Delete(chatID)
		} else if editingState.SettingName != "" {
			handleNewSettingValue(message, editingState)
			editingUsers.Delete(chatID)
		} else {
			msg := tgbotapi.NewMessage(chatID, "Please select an option from the menu.")
			if _, err := bot.Send(msg); err != nil {
				log.Printf("Failed to send message: %v", err)
			}
		}
	} else if message.IsCommand() {
		handleCommand(message)
	} else {
		msg := tgbotapi.NewMessage(chatID, "Please use the /settings commands to interact.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
	}
}

// handleCommand processes bot commands.
func handleCommand(message *tgbotapi.Message) {
	chatID := message.Chat.ID
	switch message.Command() {
	case "start":
		msg := tgbotapi.NewMessage(chatID, "Welcome! Use /settings to configure your trading options.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
	case "settings":
		showSettingsMenu(chatID)
	default:
		msg := tgbotapi.NewMessage(chatID, "Unknown command.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
	}
}

// showSettingsMenu displays the settings options to the user.
func showSettingsMenu(chatID int64) {
	settings := userSettings.Get(chatID)

	// Determine emojis for boolean settings
	autoCalcEmoji := "\U0001F6AB" // Red circle for false
	if settings.AutoCalculateTPs {
		autoCalcEmoji = "\U00002705" // Green circle for true
	}

	dynamicCalcEmoji := "\U0001F6AB" // Red circle for false
	if settings.DynamicCalculationEnabled {
		dynamicCalcEmoji = "\U00002705" // Green circle for true
	}

	toleranceEmoji := "\U0001F6AB" // Red circle for false
	if settings.EnableToleranceInMarketMode {
		toleranceEmoji = "\U00002705" // Green circle for true
	}

	// Here is the key fix: consolidate everything into a single format string
	menuText := fmt.Sprintf(
		"Your Current Settings:\n\n"+
			"<b>Margin Mode:</b> %s\n"+
			"<b>Leverage:</b> %dx\n"+
			"<b>Asset Mode:</b> %s\n"+
			"<b>Trading Mode:</b> %s\n"+
			"<b>Amount (USDT):</b> %.2f\n"+
			"<b>Use Stop Loss:</b> %t\n"+
			"<b>Simplified TP/SL:</b> %s %t\n"+
			"<b>Dynamic Calculation:</b> %s %t\n"+
			"<b>Tolerance in Market Mode:</b> %s %t\n",
		settings.MarginMode,
		settings.Leverage,
		settings.AssetMode,
		settings.TradingMode,
		settings.AmountUSDT,
		settings.UseSL,
		autoCalcEmoji,
		settings.AutoCalculateTPs,
		dynamicCalcEmoji,
		settings.DynamicCalculationEnabled,
		toleranceEmoji,
		settings.EnableToleranceInMarketMode,
	)

	// Only show Market Price Tolerance for Limit orders
	if settings.TradingMode == "Limit" {
		menuText += fmt.Sprintf("<b>Market Price Tolerance (fraction):</b> %.4f\n",
			settings.MarketPriceTolerance)
	}

	// Show TP/SL settings based on mode
	if settings.AutoCalculateTPs {
		menuText += fmt.Sprintf(
			"<b>Auto SL Percentage:</b> %.2f%%\n"+
				"<b>Auto TP Percentage:</b> %.2f%%\n",
			settings.AutoSLPercentage,
			settings.AutoTPPercentage,
		)
	} else {
		// Show TP percentages
		menuText += fmt.Sprintf("\n<b>TP1 Percentage:</b> %.2f%%\n", settings.TP1Percentage)
		if settings.TP2Enabled {
			menuText += fmt.Sprintf("<b>TP2 Percentage:</b> %.2f%%\n", settings.TP2Percentage)
		}
		if settings.TP3Enabled {
			menuText += fmt.Sprintf("<b>TP3 Percentage:</b> %.2f%%\n", settings.TP3Percentage)
		}
		menuText += fmt.Sprintf("<b>SL Percentage:</b> %.2f%%\n", settings.ManualSLPercentage)

		// Show close percentages for enabled TPs
		menuText += "\nClose Percentage for Each TP:\n"
		menuText += fmt.Sprintf("TP1: %.2f%%\n", settings.TP1ClosePct)
		if settings.TP2Enabled {
			menuText += fmt.Sprintf("TP2: %.2f%%\n", settings.TP2ClosePct)
		}
		if settings.TP3Enabled {
			menuText += fmt.Sprintf("TP3: %.2f%%\n", settings.TP3ClosePct)
		}
	}

	msg := tgbotapi.NewMessage(chatID, menuText)
	msg.ParseMode = "HTML"

	// Build keyboard
	var keyboard [][]tgbotapi.InlineKeyboardButton

	// Add base buttons
	keyboard = append(keyboard,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Margin Mode", fmt.Sprintf("%s|%s", ActionSetOption, "MarginMode")),
			tgbotapi.NewInlineKeyboardButtonData("Leverage", fmt.Sprintf("%s|%s", ActionSetOption, "Leverage")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Asset Mode", fmt.Sprintf("%s|%s", ActionSetOption, "AssetMode")),
			tgbotapi.NewInlineKeyboardButtonData("Trading Mode", fmt.Sprintf("%s|%s", ActionSetOption, "TradingMode")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Amount (USDT)", fmt.Sprintf("%s|%s", ActionSetOption, "AmountUSDT")),
			tgbotapi.NewInlineKeyboardButtonData("Use Stop Loss", fmt.Sprintf("%s|%s", ActionSetOption, "UseSL")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s Simplified TP/SL", autoCalcEmoji),
				fmt.Sprintf("%s|%s", ActionSetOption, "AutoCalculateTPs")),
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s Dynamic Calculation", dynamicCalcEmoji),
				fmt.Sprintf("%s|%s", ActionSetOption, "DynamicCalculationEnabled")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s Tolerance in Market Mode", toleranceEmoji),
				fmt.Sprintf("%s|%s", ActionSetOption, "EnableToleranceInMarketMode")),
		),
	)

	// Add Market Tolerance button only for Limit orders
	if settings.TradingMode == "Limit" {
		keyboard = append(keyboard,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Set Market Tolerance",
					fmt.Sprintf("%s|%s", ActionSetOption, "MarketPriceTolerance")),
			),
		)
	}

	// Add TP/SL buttons based on mode
	if settings.AutoCalculateTPs {
		keyboard = append(keyboard,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Set Auto SL %",
					fmt.Sprintf("%s|%s", ActionSetOption, "AutoSLPercentage")),
				tgbotapi.NewInlineKeyboardButtonData("Set Auto TP %",
					fmt.Sprintf("%s|%s", ActionSetOption, "AutoTPPercentage")),
			),
		)
	} else {
		// Always show TP1 buttons
		keyboard = append(keyboard,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Set TP1 %",
					fmt.Sprintf("%s|TP1Percentage", ActionSetOption)),
				tgbotapi.NewInlineKeyboardButtonData("TP1 Close %",
					fmt.Sprintf("%s|TP1ClosePct", ActionSetOption)),
			),
		)

		// Only show TP2 buttons if enabled
		if settings.TP2Enabled {
			keyboard = append(keyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("Set TP2 %",
						fmt.Sprintf("%s|TP2Percentage", ActionSetOption)),
					tgbotapi.NewInlineKeyboardButtonData("TP2 Close %",
						fmt.Sprintf("%s|TP2ClosePct", ActionSetOption)),
				),
			)
		}

		// Only show TP3 buttons if enabled
		if settings.TP3Enabled {
			keyboard = append(keyboard,
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("Set TP3 %",
						fmt.Sprintf("%s|TP3Percentage", ActionSetOption)),
					tgbotapi.NewInlineKeyboardButtonData("TP3 Close %",
						fmt.Sprintf("%s|TP3ClosePct", ActionSetOption)),
				),
			)
		}

		// Add SL button
		keyboard = append(keyboard,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Set SL %",
					fmt.Sprintf("%s|%s", ActionSetOption, "ManualSLPercentage")),
			),
		)
	}

	// Add Performance button
	keyboard = append(keyboard,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("View Performance",
				fmt.Sprintf("%s|%s", ActionSetOption, "ViewPerformance")),
		),
	)

	msg.ReplyMarkup = tgbotapi.InlineKeyboardMarkup{
		InlineKeyboard: keyboard,
	}

	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send settings menu: %v", err)
	}
}

// toggleToleranceInMarketMode toggles the EnableToleranceInMarketMode setting.
func toggleAutoCalculateTPs(chatID int64) {
	settings := userSettings.Get(chatID)
	settings.AutoCalculateTPs = !settings.AutoCalculateTPs
	userSettings.Set(chatID, settings)

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Auto Calculate TPs has been set to %t.", settings.AutoCalculateTPs))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send message: %v", err)
	}

	showSettingsMenu(chatID)
}

// handleCallbackQuery processes callback queries from inline keyboards.
func handleCallbackQuery(callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	chatID := callback.Message.Chat.ID
	messageID := callback.Message.MessageID

	if data == "" {
		return
	}

	parts := strings.Split(data, "|")
	if len(parts) < 2 {
		log.Printf("Invalid callback data: '%s'", data)
		return
	}

	action := parts[0]
	payload := parts[1]

	switch action {
	case ActionEdit:
		showEditOptions(chatID, messageID, payload)
	case ActionField:
		if len(parts) < 3 {
			log.Printf("Field name missing in callback data: '%s'", data)
			return
		}
		fieldName := parts[2]
		handleFieldSelection(chatID, messageID, payload, fieldName)
	case ActionConfirm:
		confirmSignal(chatID, messageID, payload)
	case ActionDismiss:
		dismissSignal(chatID, messageID, payload)
	case ActionSetOption:
		setUserOption(chatID, messageID, payload)
	case ActionChangeOption:
		if len(parts) < 3 {
			log.Printf("Option value missing in callback data: '%s'", data)
			return
		}
		value := parts[2]
		handleCallbackQueryOptionChange(chatID, payload, value)
	case ActionPerformance:
		if len(parts) < 2 {
			log.Printf("Time period missing in callback data: '%s'", data)
			return
		}
		timePeriod := parts[1]
		showPerformanceData(chatID, timePeriod)
	default:
		log.Printf("Unknown callback action: '%s'", action)
	}

	// Acknowledge callback
	callbackConfig := tgbotapi.NewCallback(callback.ID, "")
	if _, err := bot.Request(callbackConfig); err != nil {
		log.Printf("Callback acknowledgement failed: %v", err)
	}
}

// handleFieldSelection handles the selection of a field to update the entry price.
func handleFieldSelection(chatID int64, messageID int, signalID string, fieldName string) {
	signal, exists := signalStore.Get(signalID)
	if !exists {
		bot.Send(tgbotapi.NewMessage(chatID, "Signal not found."))
		return
	}

	settings := userSettings.Get(chatID)

	switch fieldName {
	case "High Price":
		signal.EntryPrice = signal.HighPrice
	case "Low Price":
		signal.EntryPrice = signal.LowPrice
	case "Midpoint":
		signal.EntryPrice = signal.Midpoint
	default:
		promptNewFieldValue(chatID, signalID, fieldName)
		return
	}

	// Recalculate TPs & SL based on entry price and user-defined percentages
	recalculateTPAndSL(signal, settings)

	// Update the Telegram message after modifications
	updatedText := constructSignalMessageText(signal)
	edit := tgbotapi.NewEditMessageText(chatID, messageID, updatedText)
	edit.ParseMode = "HTML"
	edit.ReplyMarkup = createSignalInlineKeyboard(signalID)

	if _, err := bot.Send(edit); err != nil {
		log.Printf("Failed to edit message: %v", err)
	}
}

// setUserOption handles the user's selection of a setting to change.
func setUserOption(chatID int64, messageID int, option string) {
	switch option {
	case "MarginMode":
		showMarginModeOptions(chatID, messageID)
	case "Leverage":
		promptNewSettingValue(chatID, "Leverage")
	case "AssetMode":
		showAssetModeOptions(chatID, messageID)
	case "TradingMode":
		showTradingModeOptions(chatID, messageID)
	case "AmountUSDT":
		promptNewSettingValue(chatID, "AmountUSDT")
	case "UseSL":
		toggleUseSL(chatID, messageID)
	case "AutoCalculateTPs":
		toggleAutoCalculateTPs(chatID)
	case "DynamicCalculationEnabled":
		toggleDynamicCalculation(chatID)
	case "EnableToleranceInMarketMode": // Add this case
		toggleToleranceInMarketMode(chatID)
	case "TP1Percentage":
		promptNewTPPercentage(chatID, "TP1Percentage")
	case "TP2Percentage":
		promptNewTPPercentage(chatID, "TP2Percentage")
	case "TP3Percentage":
		promptNewTPPercentage(chatID, "TP3Percentage")
	case "ManualSLPercentage":
		promptNewTPPercentage(chatID, "ManualSLPercentage")
	case "AutoSLPercentage":
		promptNewTPPercentage(chatID, "AutoSLPercentage")
	case "AutoTPPercentage":
		promptNewTPPercentage(chatID, "AutoTPPercentage")
	case "MarketPriceTolerance":
		promptNewSettingValue(chatID, "MarketPriceTolerance")
	case "TP1ClosePct":
		promptNewSettingValue(chatID, "TP1ClosePct")
	case "TP2ClosePct":
		promptNewSettingValue(chatID, "TP2ClosePct")
	case "TP3ClosePct":
		promptNewSettingValue(chatID, "TP3ClosePct")
	case "ViewPerformance":
		showPerformanceOptions(chatID)
	default:
		msg := tgbotapi.NewMessage(chatID, "Unknown setting.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
	}
}

// toggleToleranceInMarketMode toggles the EnableToleranceInMarketMode setting
func toggleToleranceInMarketMode(chatID int64) {
	settings := userSettings.Get(chatID)
	settings.EnableToleranceInMarketMode = !settings.EnableToleranceInMarketMode
	userSettings.Set(chatID, settings)

	// Send confirmation message
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Tolerance in Market Mode has been %s.",
		map[bool]string{true: "enabled", false: "disabled"}[settings.EnableToleranceInMarketMode]))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send message: %v", err)
	}

	// Show updated settings menu
	showSettingsMenu(chatID)
}

// toggleDynamicCalculation toggles the Dynamic Calculation setting.
func toggleDynamicCalculation(chatID int64) {
	settings := userSettings.Get(chatID)
	settings.DynamicCalculationEnabled = !settings.DynamicCalculationEnabled
	userSettings.Set(chatID, settings)

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Dynamic Calculation has been set to %t.", settings.DynamicCalculationEnabled))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send message: %v", err)
	}

	showSettingsMenu(chatID)
}

// promptNewTPPercentage prompts the user to enter a new percentage for TPs or SL.
func promptNewTPPercentage(chatID int64, setting string) {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Please enter the new percentage for %s (e.g., 1.5).", setting))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send prompt message: %v", err)
	}
	editingUsers.Set(chatID, &EditingState{SettingName: setting})
}

// showMarginModeOptions displays choices for Margin Mode.
func showMarginModeOptions(chatID int64, messageID int) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Cross", fmt.Sprintf("%s|MarginMode|Cross", ActionChangeOption)),
			tgbotapi.NewInlineKeyboardButtonData("Isolated", fmt.Sprintf("%s|MarginMode|Isolated", ActionChangeOption)),
		),
	)
	editMessage := tgbotapi.NewEditMessageReplyMarkup(chatID, messageID, keyboard)
	if _, err := bot.Request(editMessage); err != nil {
		log.Printf("Failed to send Margin Mode options: %v", err)
	}
}

// showAssetModeOptions displays choices for Asset Mode.
func showAssetModeOptions(chatID int64, messageID int) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Multi", fmt.Sprintf("%s|AssetMode|Multi", ActionChangeOption)),
			tgbotapi.NewInlineKeyboardButtonData("Single", fmt.Sprintf("%s|AssetMode|Single", ActionChangeOption)),
		),
	)
	editMessage := tgbotapi.NewEditMessageReplyMarkup(chatID, messageID, keyboard)
	if _, err := bot.Request(editMessage); err != nil {
		log.Printf("Failed to send Asset Mode options: %v", err)
	}
}

// showTradingModeOptions displays choices for Trading Mode.
func showTradingModeOptions(chatID int64, messageID int) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Market", fmt.Sprintf("%s|TradingMode|Market", ActionChangeOption)),
			tgbotapi.NewInlineKeyboardButtonData("Limit", fmt.Sprintf("%s|TradingMode|Limit", ActionChangeOption)),
		),
	)
	editMessage := tgbotapi.NewEditMessageReplyMarkup(chatID, messageID, keyboard)
	if _, err := bot.Request(editMessage); err != nil {
		log.Printf("Failed to send Trading Mode options: %v", err)
	}
}

// toggleUseSL toggles the UseSL boolean in settings.
func toggleUseSL(chatID int64, messageID int) {
	settings := userSettings.Get(chatID)
	settings.UseSL = !settings.UseSL
	userSettings.Set(chatID, settings)

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Use Stop Loss has been set to %t.", settings.UseSL))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send message: %v", err)
	}
	showSettingsMenu(chatID)
}

// promptNewSettingValue prompts the user to enter a new float/int for a setting.
func promptNewSettingValue(chatID int64, settingName string) {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Please enter the new value for %s.", settingName))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send prompt message: %v", err)
	}
	editingUsers.Set(chatID, &EditingState{SettingName: settingName})
}

// handleNewSettingValue sets the updated user setting from their input.
func handleNewSettingValue(message *tgbotapi.Message, editingState *EditingState) {
	chatID := message.Chat.ID
	text := strings.TrimSpace(message.Text)
	settingName := editingState.SettingName

	settings := userSettings.Get(chatID)

	// Helper function to validate and parse float
	parseFloat := func(text string, min, max float64) (float64, error) {
		val, err := strconv.ParseFloat(text, 64)
		if err != nil || val < min || val > max {
			return 0, fmt.Errorf("invalid value. Please enter a number between %.2f and %.2f", min, max)
		}
		return val, nil
	}

	switch settingName {
	case "Leverage":
		newValInt, err := strconv.Atoi(text)
		if err != nil || newValInt <= 0 || newValInt > 125 {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid leverage value. Enter a positive integer up to 125."))
			return
		}
		settings.Leverage = newValInt

	case "AmountUSDT":
		val, err := parseFloat(text, 0, 1000000)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid amount. "+err.Error()))
			return
		}
		settings.AmountUSDT = val

	case "MarketPriceTolerance":
		if settings.TradingMode == "Market" {
			bot.Send(tgbotapi.NewMessage(chatID, "Market Price Tolerance is not applicable in Market mode."))
			return
		}
		val, err := parseFloat(text, 0, 100)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid tolerance value. "+err.Error()))
			return
		}
		settings.MarketPriceTolerance = val / 100 // Convert percentage to decimal

	case "TP1ClosePct", "TP2ClosePct", "TP3ClosePct":
		val, err := parseFloat(text, 0, 100)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid percentage. "+err.Error()))
			return
		}

		switch settingName {
		case "TP1ClosePct":
			settings.TP1ClosePct = val
		case "TP2ClosePct":
			if settings.TP1ClosePct >= 100 {
				bot.Send(tgbotapi.NewMessage(chatID, "Cannot set TP2 close percentage when TP1 is 100%."))
				return
			}
			settings.TP2ClosePct = val
		case "TP3ClosePct":
			if settings.TP1ClosePct+settings.TP2ClosePct >= 100 {
				bot.Send(tgbotapi.NewMessage(chatID, "Cannot set TP3 close percentage when TP1 + TP2 is 100%."))
				return
			}
			settings.TP3ClosePct = val
		}

		// Adjust TP percentages and visibility
		adjustTPClosePercentages(settings)

	case "TP1Percentage", "TP2Percentage", "TP3Percentage":
		val, err := parseFloat(text, 0, 1000)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid percentage. "+err.Error()))
			return
		}
		switch settingName {
		case "TP1Percentage":
			settings.TP1Percentage = val
		case "TP2Percentage":
			if !settings.TP2Enabled {
				bot.Send(tgbotapi.NewMessage(chatID, "TP2 is currently disabled due to TP1 close percentage."))
				return
			}
			settings.TP2Percentage = val
		case "TP3Percentage":
			if !settings.TP3Enabled {
				bot.Send(tgbotapi.NewMessage(chatID, "TP3 is currently disabled due to TP1/TP2 close percentages."))
				return
			}
			settings.TP3Percentage = val
		}

	case "ManualSLPercentage", "AutoSLPercentage", "AutoTPPercentage":
		val, err := parseFloat(text, 0, 100)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid percentage. "+err.Error()))
			return
		}
		switch settingName {
		case "ManualSLPercentage":
			settings.ManualSLPercentage = val
		case "AutoSLPercentage":
			settings.AutoSLPercentage = val
		case "AutoTPPercentage":
			settings.AutoTPPercentage = val
		}
	}

	// Save updated settings
	userSettings.Set(chatID, settings)

	// Update the latest 20 unconfirmed signals
	unconfirmedSignals := signalStore.GetLatestUnconfirmedSignals(20)
	for _, sig := range unconfirmedSignals {
		if sig.ManualEntryEdited {
			// Keep original entry price but update TPs/SL
			originalEntry := sig.EntryPrice
			recalculateTPAndSL(sig, settings)
			sig.EntryPrice = originalEntry
		} else {
			recalculateTPAndSL(sig, settings)
		}

		// Update the Telegram message
		msgID, ok := messageStore.Get(sig.SignalID)
		if ok {
			updatedText := constructSignalMessageText(sig)
			edit := tgbotapi.NewEditMessageText(chatID, msgID, updatedText)
			edit.ParseMode = "HTML"
			edit.ReplyMarkup = createSignalInlineKeyboard(sig.SignalID)

			if _, err := bot.Send(edit); err != nil {
				log.Printf("Failed to edit message: %v", err)
			}
		}
	}

	// Acknowledge success and re-show settings
	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("%s has been updated.", settingName)))
	showSettingsMenu(chatID)
}

// adjustTPClosePercentages adjusts the TP close percentages and manages TP visibility
func adjustTPClosePercentages(settings *UserSettings) {
	// Reset all TPs to visible first
	settings.TP1Enabled = true // TP1 is always enabled
	settings.TP2Enabled = true
	settings.TP3Enabled = true

	// If TP1 is 100% or more, disable other TPs
	if settings.TP1ClosePct >= 100 {
		settings.TP1ClosePct = 100
		settings.TP2ClosePct = 0
		settings.TP3ClosePct = 0
		settings.TP2Enabled = false
		settings.TP3Enabled = false
		return
	}

	// Calculate remaining percentage after TP1
	remainingPct := 100 - settings.TP1ClosePct

	// Handle TP2
	if settings.TP2ClosePct > remainingPct {
		settings.TP2ClosePct = remainingPct
		settings.TP3ClosePct = 0
		settings.TP3Enabled = false
		return
	}

	// Calculate remaining percentage after TP2
	remainingPct -= settings.TP2ClosePct

	// Handle TP3
	if remainingPct <= 0 {
		settings.TP3ClosePct = 0
		settings.TP3Enabled = false
	} else {
		settings.TP3ClosePct = remainingPct
		settings.TP3Enabled = true
	}

	// Validate final percentages
	total := settings.TP1ClosePct + settings.TP2ClosePct + settings.TP3ClosePct
	if total > 100 {
		// If somehow total is still > 100, adjust TP3 down
		settings.TP3ClosePct = math.Max(0, 100-(settings.TP1ClosePct+settings.TP2ClosePct))
	}

	// Update TP enabled states based on final percentages
	settings.TP2Enabled = settings.TP2ClosePct > 0
	settings.TP3Enabled = settings.TP3ClosePct > 0

	// Log the adjustment results
	log.Printf("Adjusted TP percentages - TP1: %.2f%%, TP2: %.2f%%, TP3: %.2f%% (Enabled: %v, %v, %v)",
		settings.TP1ClosePct,
		settings.TP2ClosePct,
		settings.TP3ClosePct,
		settings.TP1Enabled,
		settings.TP2Enabled,
		settings.TP3Enabled)
}

// handleCallbackQueryOptionChange merges logic for direct mode changes, e.g. margin or trading mode selection.
func handleCallbackQueryOptionChange(chatID int64, key string, value string) {
	settings := userSettings.Get(chatID)

	switch key {
	case "MarginMode":
		if value != "Cross" && value != "Isolated" {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid Margin Mode selected."))
			return
		}
		settings.MarginMode = value

	case "AssetMode":
		if value != "Multi" && value != "Single" {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid Asset Mode selected."))
			return
		}
		settings.AssetMode = value

	case "TradingMode":
		if value != "Market" && value != "Limit" {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid Trading Mode selected."))
			return
		}
		settings.TradingMode = value
		// Reset Market Price Tolerance when switching to Market mode
		if value == "Market" {
			settings.MarketPriceTolerance = 0
		}

	default:
		bot.Send(tgbotapi.NewMessage(chatID, "Unknown setting."))
		return
	}

	userSettings.Set(chatID, settings)
	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("%s has been updated to %s.", key, value)))
	showSettingsMenu(chatID)
}

// confirmSignal marks a signal as confirmed and updates the message.
func confirmSignal(chatID int64, messageID int, signalID string) {
	log.Printf("confirmSignal called => chatID: %d, messageID: %d, signalID: %s", chatID, messageID, signalID)

	signal, exists := signalStore.Get(signalID)
	if !exists {
		bot.Send(tgbotapi.NewMessage(chatID, "Signal not found."))
		return
	}

	signal.Confirmed = true
	confirmationText := constructSignalMessageText(signal)
	edit := tgbotapi.NewEditMessageText(chatID, messageID, confirmationText)
	edit.ParseMode = "HTML"
	edit.ReplyMarkup = nil // remove inline keyboard on confirm

	if _, err := bot.Send(edit); err != nil {
		log.Printf("Failed to edit message: %v", err)
	}

	settings := userSettings.Get(chatID)
	err := sendToBinance(signal, settings)
	if err != nil {
		log.Printf("Failed to send signal to Binance: %v", err)
		bot.Send(tgbotapi.NewMessage(chatID, handleBinanceError(err)))
	} else {
		bot.Send(tgbotapi.NewMessage(chatID, "Trade executed on Binance successfully."))
		// Store the signal details for tracking
		trackSignal(signal)
	}
}

// trackSignal stores the signal details for later performance tracking.
func trackSignal(signal *AlertMessage) {
	if err := StoreSignal(signal.SignalID, signal.EntryPrice, signal.TP1, signal.TP2, signal.TP3, signal.SL); err != nil {
		log.Printf("Failed to store signal: %v", err)
	}
}

// dismissSignal handles the dismissal of a signal by the user.
func dismissSignal(chatID int64, messageID int, signalID string) {
	log.Printf("dismissSignal called => chatID: %d, messageID: %d, signalID: %s", chatID, messageID, signalID)

	signal, exists := signalStore.Get(signalID)
	if !exists {
		bot.Send(tgbotapi.NewMessage(chatID, "Signal not found."))
		return
	}

	signal.Dismissed = true
	dismissalText := constructSignalMessageText(signal)
	edit := tgbotapi.NewEditMessageText(chatID, messageID, dismissalText)
	edit.ParseMode = "HTML"
	edit.ReplyMarkup = nil // remove inline keyboard on dismiss

	if _, err := bot.Send(edit); err != nil {
		log.Printf("Failed to edit message: %v", err)
	}
	bot.Send(tgbotapi.NewMessage(chatID, "Signal has been dismissed."))
}

// sendToBinance sends the confirmed signal to Binance API using the user's settings.
func sendToBinance(signal *AlertMessage, settings *UserSettings) error {
	// Create a filtered signal with only enabled TPs
	filteredSignal := &AlertMessage{
		SignalID:   signal.SignalID,
		SignalType: signal.SignalType,
		Symbol:     signal.Symbol,
		EntryPrice: signal.EntryPrice,
		TP1:        signal.TP1, // TP1 is always enabled
		SL:         signal.SL,  // SL is included if UseSL is true
	}

	// Perform price tolerance check only if enabled in Market mode
	if settings.TradingMode == "Market" && settings.EnableToleranceInMarketMode {
		currentPrice, err := binanceClient.getCurrentPrice(signal.Symbol)
		if err != nil {
			return fmt.Errorf("failed to get current price: %v", err)
		}

		diff := math.Abs(currentPrice-signal.EntryPrice) / signal.EntryPrice
		if diff > settings.MarketPriceTolerance {
			return &APIError{
				Code: -4131,
				Message: fmt.Sprintf("Price difference (%.2f%%) exceeds tolerance (%.2f%%)",
					diff*100, settings.MarketPriceTolerance*100),
			}
		}
	}

	// Calculate total close percentage for validation
	totalClosePct := settings.TP1ClosePct

	// Only include TP2 if it's enabled and there's remaining percentage
	if settings.TP2Enabled && totalClosePct < 100 {
		filteredSignal.TP2 = signal.TP2
		totalClosePct += settings.TP2ClosePct
	}

	// Only include TP3 if it's enabled and there's remaining percentage
	if settings.TP3Enabled && totalClosePct < 100 {
		filteredSignal.TP3 = signal.TP3
		totalClosePct += settings.TP3ClosePct
	}

	// If totalClosePct is 100%, deactivate TP3
	if totalClosePct >= 100 {
		filteredSignal.TP3 = 0
	}

	log.Printf("Executing trade => settings: %+v, signal: %+v", settings, filteredSignal)
	return binanceClient.ExecuteTrade(filteredSignal, settings, GlobalConfig.TelegramChatID)
}

// constructSignalMessageText constructs the text of a signal message for Telegram.
func constructSignalMessageText(signal *AlertMessage) string {
	var emoji string
	if signal.SignalType == "Buy" {
		emoji = "\U0001F7E2"
	} else if signal.SignalType == "Sell" {
		emoji = "\U0001F534"
	} else {
		emoji = "\U000026AA"
	}

	msg := fmt.Sprintf("%s <b>%s Signal</b>\n\n", emoji, signal.SignalType)
	msg += fmt.Sprintf("<b>Symbol:</b> %s\n", signal.Symbol)
	msg += fmt.Sprintf("<b>Timeframe:</b> %s\n", signal.Timeframe)
	msg += fmt.Sprintf("<b>Time:</b> %s\n", signal.Time)
	msg += fmt.Sprintf("<b>Entry Price:</b> %s\n", formatFloat(signal.EntryPrice))
	msg += fmt.Sprintf("<b>TP1:</b> %s\n", formatFloat(signal.TP1))
	msg += fmt.Sprintf("<b>TP2:</b> %s\n", formatFloat(signal.TP2))
	msg += fmt.Sprintf("<b>TP3:</b> %s\n", formatFloat(signal.TP3))
	msg += fmt.Sprintf("<b>SL:</b> %s\n", formatFloat(signal.SL))
	msg += fmt.Sprintf("<b>High Price:</b> %s\n", formatFloat(signal.HighPrice))
	msg += fmt.Sprintf("<b>Low Price:</b> %s\n", formatFloat(signal.LowPrice))
	msg += fmt.Sprintf("<b>Midpoint:</b> %s\n", formatFloat(signal.Midpoint))

	if signal.Confirmed {
		msg += "\n\u2705 Signal confirmed and sent to Binance."
	} else if signal.Dismissed {
		msg += "\n\u274C Signal has been dismissed."
	}
	return msg
}

func formatFloat(num float64) string {
	if num == 0 {
		return "-"
	}
	// Convert the float to a string to determine the number of decimal places
	str := strconv.FormatFloat(num, 'f', -1, 64)
	parts := strings.Split(str, ".")
	if len(parts) == 2 {
		// If there is a decimal part, format with the same number of decimal places
		return fmt.Sprintf("%."+strconv.Itoa(len(parts[1]))+"f", num)
	}
	// If there is no decimal part, return as an integer
	return fmt.Sprintf("%.0f", num)
}

// handleNewFieldValue updates the signal's field with the new value provided by the user.
func handleNewFieldValue(message *tgbotapi.Message, editingState *EditingState) {
	chatID := message.Chat.ID
	text := strings.TrimSpace(message.Text)
	signalID := editingState.SignalID
	fieldName := editingState.Field

	// Log the signal ID and field being edited
	log.Printf("Editing signal: %s, field: %s", signalID, fieldName)

	signal, exists := signalStore.Get(signalID)
	if !exists {
		bot.Send(tgbotapi.NewMessage(chatID, "Signal not found."))
		return
	}

	settings := userSettings.Get(chatID)

	switch fieldName {
	case "Entry Price":
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "Invalid value for Entry Price."))
			return
		}

		// Update the signal's Entry Price
		signal.EntryPrice = value

		// Mark that the user manually edited this entry price
		signal.ManualEntryEdited = true

		// Recalculate TPs/SL if dynamic calculation is enabled
		recalculateTPAndSL(signal, settings)

	default:
		bot.Send(tgbotapi.NewMessage(chatID, "Unknown field."))
		return
	}

	// Update the Telegram message to reflect changes
	msgID, ok := messageStore.Get(signalID)
	if !ok {
		bot.Send(tgbotapi.NewMessage(chatID, "Unable to find the message to update."))
		return
	}

	updatedText := constructSignalMessageText(signal)
	edit := tgbotapi.NewEditMessageText(chatID, msgID, updatedText)
	edit.ParseMode = "HTML"
	edit.ReplyMarkup = createSignalInlineKeyboard(signalID)

	if _, err := bot.Send(edit); err != nil {
		log.Printf("Failed to edit message: %v", err)
		return
	}

	// Notify the user
	bot.Send(tgbotapi.NewMessage(
		chatID,
		fmt.Sprintf(
			"Signal updated successfully.\nSymbol: %s\nTime: %s\nField: %s\nNew Value: %s",
			signal.Symbol,
			signal.Time,
			fieldName,
			text,
		),
	))
}

// recalculateTPAndSL recalculates TP1, TP2, TP3, and SL based on entry price & user-defined percentages.
func recalculateTPAndSL(signal *AlertMessage, settings *UserSettings) {
	if !settings.DynamicCalculationEnabled {
		// If dynamic calculation is disabled, use the exact values from the alert
		return
	}

	entryPrice := signal.EntryPrice
	if entryPrice <= 0 {
		return
	}

	if settings.AutoCalculateTPs {
		// Simplified TP/SL is true
		if signal.SignalType == "Buy" {
			// For Buy signals, TP1 is above entry and SL is below entry
			signal.TP1 = roundToSixDecimal(entryPrice * (1 + (settings.AutoTPPercentage / 100.0)))
			if settings.UseSL {
				signal.SL = roundToSixDecimal(entryPrice * (1 - (settings.AutoSLPercentage / 100.0)))
			}
		} else if signal.SignalType == "Sell" {
			// For Sell signals, TP1 is below entry and SL is above entry
			signal.TP1 = roundToSixDecimal(entryPrice * (1 - (settings.AutoTPPercentage / 100.0)))
			if settings.UseSL {
				signal.SL = roundToSixDecimal(entryPrice * (1 + (settings.AutoSLPercentage / 100.0)))
			}
		}
	} else {
		// Simplified TP/SL is false
		if signal.SignalType == "Buy" {
			// For Buy signals, TPs are above entry
			signal.TP1 = roundToSixDecimal(entryPrice * (1 + (settings.TP1Percentage / 100.0)))
			signal.TP2 = roundToSixDecimal(entryPrice * (1 + (settings.TP2Percentage / 100.0)))
			signal.TP3 = roundToSixDecimal(entryPrice * (1 + (settings.TP3Percentage / 100.0)))

			// SL is below entry, only if UseSL is turned on
			if settings.UseSL {
				signal.SL = roundToSixDecimal(entryPrice * (1 - (settings.ManualSLPercentage / 100.0)))
			}
		} else if signal.SignalType == "Sell" {
			// For Sell signals, TPs are below entry
			signal.TP1 = roundToSixDecimal(entryPrice * (1 - (settings.TP1Percentage / 100.0)))
			signal.TP2 = roundToSixDecimal(entryPrice * (1 - (settings.TP2Percentage / 100.0)))
			signal.TP3 = roundToSixDecimal(entryPrice * (1 - (settings.TP3Percentage / 100.0)))

			if settings.UseSL {
				signal.SL = roundToSixDecimal(entryPrice * (1 + (settings.ManualSLPercentage / 100.0)))
			}
		}
	}
}

// roundToSixDecimal rounds a float64 to six decimal places.
func roundToSixDecimal(num float64) float64 {
	return math.Round(num*1000000) / 1000000
}

// showEditOptions displays fields that can be edited for a signal.
func showEditOptions(chatID int64, messageID int, signalID string) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Entry Price", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "Entry Price")),
			tgbotapi.NewInlineKeyboardButtonData("SL", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "SL")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("TP1", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "TP1")),
			tgbotapi.NewInlineKeyboardButtonData("TP2", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "TP2")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("TP3", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "TP3")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Set High Price", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "High Price")),
			tgbotapi.NewInlineKeyboardButtonData("Set Low Price", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "Low Price")),
			tgbotapi.NewInlineKeyboardButtonData("Set Midpoint", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "Midpoint")),
		),
	)

	editMessage := tgbotapi.NewEditMessageReplyMarkup(chatID, messageID, keyboard)
	if _, err := bot.Request(editMessage); err != nil {
		log.Printf("Failed to send edit options: %v", err)
	}
}

// promptNewFieldValue prompts the user to enter a new value for a specific signal field.
func promptNewFieldValue(chatID int64, signalID string, fieldName string) {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Please enter the new value for %s.", fieldName))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send prompt: %v", err)
	}
	editingUsers.Set(chatID, &EditingState{SignalID: signalID, Field: fieldName})
}

// createSignalInlineKeyboard creates the inline keyboard for a signal message (Edit, Confirm, Dismiss, High, Low, Midpoint).
func createSignalInlineKeyboard(signalID string) *tgbotapi.InlineKeyboardMarkup {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Edit", fmt.Sprintf("%s|%s", ActionEdit, signalID)),
			tgbotapi.NewInlineKeyboardButtonData("Confirm", fmt.Sprintf("%s|%s", ActionConfirm, signalID)),
			tgbotapi.NewInlineKeyboardButtonData("Dismiss", fmt.Sprintf("%s|%s", ActionDismiss, signalID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Set High Price", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "High Price")),
			tgbotapi.NewInlineKeyboardButtonData("Set Low Price", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "Low Price")),
			tgbotapi.NewInlineKeyboardButtonData("Set Midpoint", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "Midpoint")),
		),
	)
	return &keyboard
}

// sendSignalMessage sends an alert message to the Telegram chat.
func sendSignalMessage(alert *AlertMessage) (int, error) {
	chatID := GlobalConfig.TelegramChatID
	if chatID == 0 {
		return 0, fmt.Errorf("Telegram Chat ID is not set in your config.")
	}

	signalID := alert.SignalID
	signalID = sanitizeSignalID(signalID)

	settings := userSettings.Get(chatID)
	// If dynamic calculation is enabled and alert has a nonzero entry, recalc TPs & SL:
	if settings.DynamicCalculationEnabled && alert.EntryPrice > 0 {
		recalculateTPAndSL(alert, settings)
	}

	signalStore.Set(signalID, alert)

	messageText := constructSignalMessageText(alert)
	msg := tgbotapi.NewMessage(chatID, messageText)
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = createSignalInlineKeyboard(signalID)

	sentMessage, err := bot.Send(msg)
	if err != nil {
		return 0, fmt.Errorf("failed to send signal message: %v", err)
	}

	messageStore.Set(signalID, sentMessage.MessageID)
	return sentMessage.MessageID, nil
}

// sanitizeSignalID sanitizes the signal ID to ensure it is safe for usage in callback data.
func sanitizeSignalID(signalID string) string {
	re := regexp.MustCompile(`\W`)
	return re.ReplaceAllString(signalID, "")
}

// handleBinanceError provides a user-friendly error message for Binance API errors.
func handleBinanceError(err error) string {
	apiErr, ok := err.(*APIError)
	if !ok {
		// If the error is not of type APIError, just return a generic message.
		return "An unexpected error occurred. Please try again later."
	}

	switch apiErr.Code {
	case -4131:
		return "Trade could not be placed because the requested price is outside Binance's allowable range. Please move closer to the current market price and try again."
	default:
		// Instead of returning the raw message, return a generic text to users.
		return "An unexpected error occurred. Please try again later."
	}
}

// APIError represents an error from the Binance API
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("code=%d, msg=%s", e.Code, e.Message)
}

// showPerformanceOptions displays performance options for different time periods.
func showPerformanceOptions(chatID int64) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Previous Day", "performance|day"),
			tgbotapi.NewInlineKeyboardButtonData("Previous Week", "performance|week"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Previous Month", "performance|month"),
			tgbotapi.NewInlineKeyboardButtonData("Previous Year", "performance|year"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Recent Years", "performance|years"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "Select the time period for performance data:")
	msg.ReplyMarkup = keyboard
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send performance options: %v", err)
	}
}

// showPerformanceData fetches and displays performance data for a given time period.
func showPerformanceData(chatID int64, timePeriod string) {
	trades, err := GetTradesForPeriod(timePeriod)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Failed to fetch trade data: %v", err)))
		return
	}

	performanceData := calculatePerformanceMetrics(trades)

	var title string
	switch timePeriod {
	case "day":
		title = "Performance Summary for Previous Day"
	case "week":
		title = "Performance Summary for Previous Week"
	case "month":
		title = "Performance Summary for Previous Month"
	case "year":
		title = "Performance Summary for Previous Year"
	default:
		title = "Performance Summary"
	}

	msgText := fmt.Sprintf("%s:\n%s", title, formatPerformanceData(performanceData))
	msg := tgbotapi.NewMessage(chatID, msgText)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send performance data: %v", err)
	}
}

// Calculate Performance Metrics
func calculatePerformanceMetrics(trades []Trade) PerformanceData {
	var totalTrades, winningTrades, losingTrades int
	var totalProfit, totalLoss float64

	for _, trade := range trades {
		totalTrades++
		if trade.Profit > 0 {
			winningTrades++
			totalProfit += trade.Profit
		} else {
			losingTrades++
			totalLoss += trade.Profit
		}
	}

	winLossRatio := float64(winningTrades) / float64(totalTrades)
	averageProfit := totalProfit / float64(winningTrades)
	averageLoss := totalLoss / float64(losingTrades)
	netProfit := totalProfit + totalLoss

	return PerformanceData{
		TotalTrades:   totalTrades,
		WinningTrades: winningTrades,
		LosingTrades:  losingTrades,
		WinLossRatio:  winLossRatio,
		AverageProfit: averageProfit,
		AverageLoss:   averageLoss,
		TotalProfit:   totalProfit,
		TotalLoss:     totalLoss,
		NetProfit:     netProfit,
	}
}

// Example function to format performance data for display
func formatPerformanceData(data PerformanceData) string {
	return fmt.Sprintf(
		"Performance Summary:\n"+
			"Total Trades: %d\n"+
			"Winning Trades: %d\n"+
			"Losing Trades: %d\n"+
			"Win/Loss Ratio: %.2f\n"+
			"Average Profit: %.2f\n"+
			"Average Loss: %.2f\n"+
			"Total Profit: %.2f\n"+
			"Total Loss: %.2f\n"+
			"Net Profit: %.2f\n",
		data.TotalTrades,
		data.WinningTrades,
		data.LosingTrades,
		data.WinLossRatio,
		data.AverageProfit,
		data.AverageLoss,
		data.TotalProfit,
		data.TotalLoss,
		data.NetProfit,
	)
}
