package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
)

const (
	// ServerPort is the port the server listens on
	ServerPort = "8000"
)

func init() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Error loading .env file")
	}
}

func main() {
	// Initialize the database
	if err := initDatabase(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Load the initial configuration
	config, err := getConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Set the global configuration
	SetGlobalConfig(*config)

	// Initialize admin components (session store and templates)
	initAdmin()

	// Retrieve and decode CSRF_AUTH_KEY
	csrfKeyHex := os.Getenv("CSRF_AUTH_KEY")
	if csrfKeyHex == "" {
		log.Fatal("CSRF_AUTH_KEY environment variable is not set")
	}

	csrfKey, err := hex.DecodeString(csrfKeyHex)
	if err != nil || len(csrfKey) != 32 {
		log.Fatal("CSRF_AUTH_KEY must be a 64-character hexadecimal string representing 32 bytes")
	}

	// CSRF protection middleware
	csrfMiddleware := csrf.Protect(csrfKey, csrf.Secure(false)) // Set csrf.Secure(true) if using HTTPS

	// Initialize the Telegram bot if the configuration is set
	if GlobalConfig.TelegramBotToken != "" && GlobalConfig.TelegramChatID != 0 {
		bot, err = initTelegramBot(&GlobalConfig)
		if err != nil {
			log.Printf("Telegram bot not initialized: %v", err)
		} else {
			// Start the Telegram listener
			startTelegramListener()
		}
	} else {
		log.Println("Telegram configuration is not set. Please configure via the admin panel.")
	}

	// Create a new router
	r := mux.NewRouter()

	// Serve static files from /admin/assets/
	r.PathPrefix("/admin/assets/").Handler(
		http.StripPrefix(
			"/admin/assets/",
			http.FileServer(http.Dir("./assets")),
		),
	)

	// Admin routes with CSRF protection
	r.Handle("/admin/login", csrfMiddleware(http.HandlerFunc(adminLoginHandler)))
	r.Handle("/admin/config", csrfMiddleware(http.HandlerFunc(adminConfigHandler)))

	// Webhook handler
	r.HandleFunc("/webhook", webhookHandler)

	// Root URL redirection
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
	})

	// Create the server
	server := &http.Server{
		Addr:         ":" + ServerPort,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start the server in a goroutine
	go func() {
		log.Println("Starting server on port", ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	// Stop the Telegram listener if it's running
	stopTelegramListener()

	log.Println("Server exited properly")
}

// webhookHandler handles incoming webhook requests.
func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// Read the request body
	body, err := io.ReadAll(io.LimitReader(r.Body, 1048576)) // Limit the size to prevent abuse
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse the JSON alert message
	var alert AlertMessage
	if err := json.Unmarshal(body, &alert); err != nil {
		log.Printf("JSON Unmarshal error: %v", err)
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if alert.SignalID == "" || alert.Symbol == "" || alert.Time == "" {
		log.Printf("Invalid alert data: missing required fields")
		http.Error(w, "Invalid alert data: missing required fields", http.StatusBadRequest)
		return
	}

	log.Printf("Received alert: %+v", alert)

	// Send the message to Telegram
	if _, err := sendSignalMessage(&alert); err != nil {
		log.Printf("Failed to send message to Telegram: %v", err)
		http.Error(w, "Failed to send message to Telegram", http.StatusInternalServerError)
		return
	}

	// Respond to the webhook sender
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Alert received and processed"))
}
