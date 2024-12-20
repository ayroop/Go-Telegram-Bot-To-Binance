// telegram.go
package main

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var bot *tgbotapi.BotAPI

// Synchronization primitives
var (
	editingUsers     = NewEditingUsers()
	signalStore      = NewSignalStore()
	messageStore     = NewMessageStore()
	updatesChan      tgbotapi.UpdatesChannel
	listenerShutdown chan struct{}
)

// Constants for action types
const (
	ActionEdit    = "edit"
	ActionField   = "field"
	ActionConfirm = "conf"
)

// EditingState represents the state of a user editing a signal.
type EditingState struct {
	SignalID string
	Field    string
}

// EditingUsers manages the editing state of users with concurrency safety.
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

func (e *EditingUsers) Get(chatID int64) (*EditingState, bool) {
	e.RLock()
	defer e.RUnlock()
	state, exists := e.users[chatID]
	return state, exists
}

func (e *EditingUsers) Set(chatID int64, state *EditingState) {
	e.Lock()
	defer e.Unlock()
	e.users[chatID] = state
}

func (e *EditingUsers) Delete(chatID int64) {
	e.Lock()
	defer e.Unlock()
	delete(e.users, chatID)
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

func (s *SignalStore) Get(signalID string) (*AlertMessage, bool) {
	s.RLock()
	defer s.RUnlock()
	signal, exists := s.signals[signalID]
	return signal, exists
}

func (s *SignalStore) Set(signalID string, signal *AlertMessage) {
	s.Lock()
	defer s.Unlock()
	s.signals[signalID] = signal
}

// MessageStore manages message IDs with concurrency safety.
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

func (m *MessageStore) Get(signalID string) (int, bool) {
	m.RLock()
	defer m.RUnlock()
	messageID, exists := m.messages[signalID]
	return messageID, exists
}

func (m *MessageStore) Set(signalID string, messageID int) {
	m.Lock()
	defer m.Unlock()
	m.messages[signalID] = messageID
}

// AlertMessage represents a signal or alert.
type AlertMessage struct {
	SignalID   string  `json:"signal_id"`
	Signal     string  `json:"signal"`
	Symbol     string  `json:"symbol"`
	Timeframe  string  `json:"timeframe"`
	Time       string  `json:"time"`
	EntryPrice float64 `json:"entry_price"`
	TP1        float64 `json:"tp1"`
	TP2        float64 `json:"tp2"`
	TP3        float64 `json:"tp3"`
	TP4        float64 `json:"tp4"`
	SL         float64 `json:"sl"`
	HighPrice  float64 `json:"high_price"`
	LowPrice   float64 `json:"low_price"`
	Midpoint   float64 `json:"midpoint"`
	Confirmed  bool    `json:"confirmed"`
}

// initTelegramBot initializes the Telegram bot.
func initTelegramBot(config *Config) error {
	// Stop previously running bot and listener if any
	if bot != nil {
		// Log the re-initialization
		log.Println("Re-initializing Telegram bot with new configuration.")

		// Stop the listener
		stopTelegramListener()

		// Set bot to nil
		bot = nil
	}

	if config.TelegramBotToken == "" {
		return fmt.Errorf("Telegram Bot Token is not set")
	}

	var err error
	bot, err = tgbotapi.NewBotAPI(config.TelegramBotToken)
	if err != nil {
		return fmt.Errorf("failed to create Telegram bot: %v", err)
	}
	bot.Debug = false
	log.Printf("Authorized on account %s", bot.Self.UserName)

	// Start the listener
	startTelegramListener()

	return nil
}

// startTelegramListener starts the listener for Telegram updates.
func startTelegramListener() {
	// If updatesChan is already running, return
	if updatesChan != nil {
		return
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updatesChan = bot.GetUpdatesChan(u)

	// Initialize the shutdown channel
	listenerShutdown = make(chan struct{})

	go func() {
		defer func() {
			// Signal that the listener has stopped
			close(listenerShutdown)
		}()
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
		// Stop receiving updates
		bot.StopReceivingUpdates()
		// Wait for the listener goroutine to finish
		<-listenerShutdown
		updatesChan = nil
		listenerShutdown = nil
	}
}

// handleMessage processes incoming messages.
func handleMessage(message *tgbotapi.Message) {
	chatID := message.Chat.ID

	editingState, editing := editingUsers.Get(chatID)

	if editing && editingState.Field != "" {
		handleNewFieldValue(message, editingState)
		editingUsers.Delete(chatID)
	} else if message.IsCommand() {
		handleCommand(message)
	} else if editing {
		msg := tgbotapi.NewMessage(chatID, "Please select a field to edit from the options.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
	}
}

// handleCallbackQuery processes callback queries from inline keyboards.
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
	default:
		log.Printf("Unknown callback action: '%s'", action)
	}

	// Acknowledge the callback
	callbackConfig := tgbotapi.NewCallback(callback.ID, "")
	if _, err := bot.Request(callbackConfig); err != nil {
		log.Printf("Callback acknowledgement failed: %v", err)
	}
}

// showEditOptions displays inline keyboard options for editing fields.
func showEditOptions(chatID int64, messageID int, signalID string) {
	if signalID == "" {
		log.Printf("Error: signalID is empty in showEditOptions")
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Entry Price", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "EntryPrice")),
			tgbotapi.NewInlineKeyboardButtonData("TP1", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "TP1")),
			tgbotapi.NewInlineKeyboardButtonData("TP2", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "TP2")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("TP3", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "TP3")),
			tgbotapi.NewInlineKeyboardButtonData("TP4", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "TP4")),
			tgbotapi.NewInlineKeyboardButtonData("SL", fmt.Sprintf("%s|%s|%s", ActionField, signalID, "SL")),
		),
	)

	editMessage := tgbotapi.NewEditMessageReplyMarkup(chatID, messageID, keyboard)
	if _, err := bot.Request(editMessage); err != nil {
		log.Printf("Failed to send edit options: %v", err)
	}

	editingUsers.Set(chatID, &EditingState{SignalID: signalID})
}

// promptNewFieldValue prompts the user to enter a new value for a field.
func promptNewFieldValue(chatID int64, signalID string, field string) {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Please enter the new value for %s.", field))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send prompt message: %v", err)
	}

	editingUsers.Set(chatID, &EditingState{SignalID: signalID, Field: field})
}

// handleNewFieldValue handles the new value provided by the user for a field.
func handleNewFieldValue(message *tgbotapi.Message, editingState *EditingState) {
	chatID := message.Chat.ID
	text := strings.TrimSpace(message.Text)
	field := editingState.Field
	signalID := editingState.SignalID

	log.Printf("User %d is updating signalID: '%s', field: '%s', newValue: '%s'", chatID, signalID, field, text)

	signal, exists := signalStore.Get(signalID)
	if !exists {
		msg := tgbotapi.NewMessage(chatID, "Signal not found.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
		return
	}

	// Try converting the input to float64
	newValue, err := strconv.ParseFloat(text, 64)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Invalid value. Please enter a numeric value.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
		return
	}

	// Update the corresponding field
	switch field {
	case "EntryPrice":
		signal.EntryPrice = newValue
	case "TP1":
		signal.TP1 = newValue
	case "TP2":
		signal.TP2 = newValue
	case "TP3":
		signal.TP3 = newValue
	case "TP4":
		signal.TP4 = newValue
	case "SL":
		signal.SL = newValue
	default:
		msg := tgbotapi.NewMessage(chatID, "Unknown field.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
		return
	}

	// Acknowledge the update
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("%s has been updated to %s.", field, formatFloat(newValue)))
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Failed to send message: %v", err)
	}

	// Update the original message in the chat
	messageID, exists := messageStore.Get(signalID)
	if exists {
		updatedMessageText := constructSignalMessageText(signal)
		edit := tgbotapi.NewEditMessageText(chatID, messageID, updatedMessageText)
		edit.ParseMode = "HTML"

		// Re-attach the inline keyboard if not confirmed
		if !signal.Confirmed {
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("Edit", fmt.Sprintf("%s|%s", ActionEdit, signalID)),
					tgbotapi.NewInlineKeyboardButtonData("Confirm", fmt.Sprintf("%s|%s", ActionConfirm, signalID)),
				),
			)
			edit.ReplyMarkup = &keyboard
		}

		if _, err := bot.Send(edit); err != nil {
			log.Printf("Failed to edit message: %v", err)
		}
	}
}

// confirmSignal marks a signal as confirmed and updates the message.
func confirmSignal(chatID int64, messageID int, signalID string, callback *tgbotapi.CallbackQuery) {
	log.Printf("confirmSignal called with chatID: %d, messageID: %d, signalID: %s", chatID, messageID, signalID)

	signal, exists := signalStore.Get(signalID)
	if !exists {
		msg := tgbotapi.NewMessage(chatID, "Signal not found.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
		return
	}

	signal.Confirmed = true

	// Construct the confirmation message with the latest signal data
	confirmationText := constructSignalMessageText(signal)

	// Edit the original message to reflect the confirmation
	edit := tgbotapi.NewEditMessageText(chatID, messageID, confirmationText)
	edit.ParseMode = "HTML"

	// Remove the inline keyboard
	edit.ReplyMarkup = nil

	if _, err := bot.Send(edit); err != nil {
		log.Printf("Failed to edit message: %v", err)
	}
}

// handleCommand processes bot commands.
func handleCommand(message *tgbotapi.Message) {
	switch message.Command() {
	case "start":
		msg := tgbotapi.NewMessage(message.Chat.ID, "Welcome! Use the inline buttons to interact with signals.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
	default:
		msg := tgbotapi.NewMessage(message.Chat.ID, "Unknown command.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Failed to send message: %v", err)
		}
	}
}

// constructSignalMessageText formats the signal data into a message string.
func constructSignalMessageText(signal *AlertMessage) string {
	// Use different emojis for Buy and Sell signals using Unicode escape sequences
	var signalEmoji string
	switch strings.ToLower(signal.Signal) {
	case "buy":
		signalEmoji = "\U0001F7E2" // Green circle
	case "sell":
		signalEmoji = "\U0001F534" // Red circle
	default:
		signalEmoji = ""
	}

	// Ensure fields have default values if empty
	defaultString := "N/A"

	// Use Unicode escape sequence for "?"
	confirmedEmoji := "\u2705" // ?

	message := fmt.Sprintf(
		`%s <b>%s Signal</b>

<b>Symbol:</b> %s
<b>Timeframe:</b> %s
<b>Time:</b> %s
<b>Entry Price:</b> %s
<b>TP1:</b> %s
<b>TP2:</b> %s
<b>TP3:</b> %s
<b>TP4:</b> %s
<b>SL:</b> %s
<b>High Price:</b> %s
<b>Low Price:</b> %s
<b>Midpoint:</b> %s`,
		signalEmoji,
		nonEmptyString(signal.Signal, defaultString),
		nonEmptyString(signal.Symbol, defaultString),
		nonEmptyString(signal.Timeframe, defaultString),
		nonEmptyString(signal.Time, defaultString),
		formatFloat(signal.EntryPrice),
		formatFloat(signal.TP1),
		formatFloat(signal.TP2),
		formatFloat(signal.TP3),
		formatFloat(signal.TP4),
		formatFloat(signal.SL),
		formatFloat(signal.HighPrice),
		formatFloat(signal.LowPrice),
		formatFloat(signal.Midpoint),
	)

	if signal.Confirmed {
		message += fmt.Sprintf("\n\n<b>Status:</b> Confirmed %s", confirmedEmoji)
	}

	return message
}

// nonEmptyString returns the string if it's not empty; otherwise, returns the default value.
func nonEmptyString(str, defaultVal string) string {
	if str == "" {
		return defaultVal
	}
	return str
}

// formatFloat formats a float64 value into a string, preserving the exact input.
func formatFloat(value float64) string {
	if value == 0 {
		return "N/A"
	}
	// Format the float to preserve the exact input
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// sendSignalMessage sends a new signal message to the Telegram chat.
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
	signalStore.Set(signalID, &alert)

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

// sanitizeSignalID cleans the signal ID to ensure it's safe to use.
func sanitizeSignalID(signalID string) string {
	// Replace any non-word characters with underscores
	re := regexp.MustCompile(`[^\w]+`)
	sanitizedID := re.ReplaceAllString(signalID, "_")
	sanitizedID = strings.Trim(sanitizedID, "_")
	if sanitizedID == "" {
		sanitizedID = fmt.Sprintf("signal_%d", time.Now().UnixNano())
	}
	return sanitizedID
}