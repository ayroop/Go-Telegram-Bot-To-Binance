// admin.go
package main

import (
    "crypto/subtle"
    "html/template"
    "log"
    "net/http"
    "os"
    "strconv"

    "github.com/gorilla/csrf"
    "github.com/gorilla/sessions"
    "golang.org/x/crypto/bcrypt"
)

// Session management
var store *sessions.CookieStore

// Templates
var templates *template.Template

// Admin credentials
const (
    adminUsername = "admin"
)

// ConfigPageData holds data passed to the config template
type ConfigPageData struct {
    CSRFToken         string
    CSRFTemplateField template.HTML
    Config            Config
    ErrorMessage      string
    SuccessMessage    string
}

// LoginPageData holds data passed to the login template
type LoginPageData struct {
    CSRFToken         string
    CSRFTemplateField template.HTML
    ErrorMessage      string
}

// initAdmin initializes session store and templates.
func initAdmin() {
    // Use a persistent session secret from an environment variable
    sessionSecret := os.Getenv("SESSION_SECRET")
    if sessionSecret == "" {
        log.Fatal("SESSION_SECRET environment variable is not set")
    }
    store = sessions.NewCookieStore([]byte(sessionSecret))

    // Optional: Set secure session options
    store.Options = &sessions.Options{
        HttpOnly: true,
        Path:     "/",
        Secure:   true, // Set to true if using HTTPS
    }

    // Load templates
    var err error
    templates, err = template.ParseFiles("templates/login.html", "templates/config.html")
    if err != nil {
        log.Fatalf("Error parsing templates: %v", err)
    }
}

// adminLoginHandler handles the admin login page.
func adminLoginHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodGet {
        data := LoginPageData{
            CSRFToken:         csrf.Token(r),
            CSRFTemplateField: csrf.TemplateField(r),
        }
        // Render the login page
        if err := templates.ExecuteTemplate(w, "login.html", data); err != nil {
            log.Printf("Error rendering login template: %v", err)
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        }
        return
    }

    // Process login form submission
    if err := r.ParseForm(); err != nil {
        http.Error(w, "Invalid form data", http.StatusBadRequest)
        return
    }

    username := r.FormValue("username")
    password := r.FormValue("password")

    // Authenticate user
    if authenticateUser(username, password) {
        session, _ := store.Get(r, "session-name")
        session.Values["authenticated"] = true
        if err := session.Save(r, w); err != nil {
            log.Printf("Error saving session: %v", err)
        }
        http.Redirect(w, r, "/admin/config", http.StatusSeeOther)
    } else {
        data := LoginPageData{
            CSRFToken:         csrf.Token(r),
            CSRFTemplateField: csrf.TemplateField(r),
            ErrorMessage:      "Invalid credentials",
        }
        if err := templates.ExecuteTemplate(w, "login.html", data); err != nil {
            log.Printf("Error rendering login template: %v", err)
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        }
    }
}

// adminConfigHandler handles the configuration page.
func adminConfigHandler(w http.ResponseWriter, r *http.Request) {
    // Check if user is authenticated
    session, _ := store.Get(r, "session-name")
    auth, ok := session.Values["authenticated"].(bool)
    if !ok || !auth {
        http.Redirect(w, r, "/admin/login", http.StatusFound)
        return
    }

    if r.Method == http.MethodGet {
        // Fetch current config from the database
        config, err := getConfig()
        if err != nil {
            log.Printf("Error fetching config: %v", err)
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
            return
        }
        data := ConfigPageData{
            CSRFToken:         csrf.Token(r),
            CSRFTemplateField: csrf.TemplateField(r),
            Config:            *config,
        }
        if err := templates.ExecuteTemplate(w, "config.html", data); err != nil {
            log.Printf("Error rendering config template: %v", err)
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        }
        return
    }

    // Process config form submission
    if err := r.ParseForm(); err != nil {
        http.Error(w, "Invalid form data", http.StatusBadRequest)
        return
    }

    botToken := r.FormValue("bot_token")
    chatIDStr := r.FormValue("chat_id")

    // Validate inputs
    if botToken == "" || chatIDStr == "" {
        data := ConfigPageData{
            CSRFToken:         csrf.Token(r),
            CSRFTemplateField: csrf.TemplateField(r),
            ErrorMessage:      "All fields are required",
            Config: Config{
                TelegramBotToken: botToken,
                // Include previously entered data to retain user input
            },
        }
        if err := templates.ExecuteTemplate(w, "config.html", data); err != nil {
            log.Printf("Error rendering config template: %v", err)
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        }
        return
    }

    // Convert chat ID to int64
    chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
    if err != nil {
        data := ConfigPageData{
            CSRFToken:         csrf.Token(r),
            CSRFTemplateField: csrf.TemplateField(r),
            ErrorMessage:      "Invalid Chat ID",
            Config: Config{
                TelegramBotToken: botToken,
                // Include previously entered data to retain user input
            },
        }
        if err := templates.ExecuteTemplate(w, "config.html", data); err != nil {
            log.Printf("Error rendering config template: %v", err)
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        }
        return
    }

    // Save config to the database
    newConfig := Config{
        TelegramBotToken: botToken,
        TelegramChatID:   chatID,
    }
    err = saveConfig(&newConfig)
    if err != nil {
        log.Printf("Error saving config: %v", err)
        http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        return
    }

    // Update GlobalConfig
    SetGlobalConfig(newConfig)

    // Re-initialize the Telegram bot
    if err := initTelegramBot(&newConfig); err != nil {
        log.Printf("Failed to initialize Telegram bot: %v", err)
        data := ConfigPageData{
            CSRFToken:         csrf.Token(r),
            CSRFTemplateField: csrf.TemplateField(r),
            Config:            newConfig,
            ErrorMessage:      "Configuration updated, but failed to initialize Telegram bot.",
        }
        if err := templates.ExecuteTemplate(w, "config.html", data); err != nil {
            log.Printf("Error rendering config template: %v", err)
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        }
        return
    }

    data := ConfigPageData{
        CSRFToken:         csrf.Token(r),
        CSRFTemplateField: csrf.TemplateField(r),
        Config:            newConfig,
        SuccessMessage:    "Configuration updated successfully",
    }
    if err := templates.ExecuteTemplate(w, "config.html", data); err != nil {
        log.Printf("Error rendering config template: %v", err)
        http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    }
}

// authenticateUser verifies the provided credentials.
func authenticateUser(username, password string) bool {
    // Securely compare usernames
    usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(adminUsername)) == 1
    if !usernameMatch {
        return false
    }

    // Retrieve the hashed password from environment variable
    hashedPassword := os.Getenv("ADMIN_PASSWORD_HASH")
    if hashedPassword == "" {
        log.Fatal("ADMIN_PASSWORD_HASH environment variable is not set")
    }

    // Compare hashed passwords
    err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
    if err != nil {
        // Password does not match
        return false
    }

    return true
}