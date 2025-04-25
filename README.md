# Go Telegram Trading Bot To Binance

![Go Version](https://img.shields.io/badge/Go-1.17%2B-blue)
![License](https://img.shields.io/badge/License-MIT-green)

A production-ready Go application that integrates with Telegram to manage trading signals from TradingView and execute trades on Binance. The application provides a secure admin panel for configuration and a Telegram bot interface for signal management.

## 📋 Features

- **TradingView Integration**: Receive alerts via webhook and process them into actionable trading signals
- **Telegram Bot**: Interactive interface to edit entry prices, take profits (TPs), stop loss (SL), and confirm signals
- **Admin Panel**: Secure web interface for configuring the bot and application settings
- **Binance Trading**: Execute trades on Binance based on confirmed signals
- **Security**: Robust session management, CSRF protection, and secure credential storage

## 🏗️ Project Structure

```
.
├── admin.go              # Admin panel HTTP handlers
├── assets/               # CSS/JS assets for admin panel
├── binance_trade.go      # Binance integration (API clients, trading logic)
├── config.go             # Configuration handling
├── database.go           # SQLite database helpers
├── go.mod/go.sum         # Go modules
├── main.go               # App entrypoint
├── telegram.go           # Telegram bot logic
├── templates/            # Admin panel HTML templates
├── .gitignore            # Specifies files/folders not to track
└── README.md             # Project documentation
```

## 🚀 Getting Started

### Prerequisites

- **Go** version 1.17 or later
- **SQLite** installed (for database)
- **Telegram Bot Token** from [BotFather](https://core.telegram.org/bots#6-botfather)
- **Binance API Keys** (for trading functionality)

### Installation

1. **Clone the repository**
   ```bash
   git clone https://github.com/ayroop/Go-Telegram-Trading-Bot.git
   cd Go-Telegram-Trading-Bot
   ```

2. **Install dependencies**
   ```bash
   go mod tidy
   go mod vendor  # Optional
   ```

## ⚙️ Configuration

### Required Environment Variables

The application requires these environment variables:

- `SESSION_SECRET`: Random string for session security
- `CSRF_AUTH_KEY`: 64-character hex string for CSRF protection
- `ADMIN_PASSWORD_HASH`: bcrypt hash of admin password

### Generating Security Keys

1. **Generate SESSION_SECRET and CSRF_AUTH_KEY**
   ```bash
   openssl rand -hex 32
   ```

2. **Generate ADMIN_PASSWORD_HASH**
   Create a file named `generate_hash.go`:

   ```go
   package main

   import (
       "fmt"
       "golang.org/x/crypto/bcrypt"
   )

   func main() {
       password := "your_admin_password" // Replace with your actual password
       hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
       if err != nil {
           fmt.Println("Error generating password hash:", err)
           return
       }
       fmt.Println(string(hashedPassword))
   }
   ```

   Run it:
   ```bash
   go run generate_hash.go
   ```

### Development Setup with .env

Create a `.env` file for local development:

```
SESSION_SECRET=your-generated-session-secret
CSRF_AUTH_KEY=your-generated-csrf-key
ADMIN_PASSWORD_HASH=your-generated-password-hash
```

**⚠️ IMPORTANT: Never commit your `.env` file to version control!**

## 🛠️ Building and Running

### Build the Application

```bash
go build -o app
```

### Run Locally

```bash
# Load environment variables from .env (development only)
export $(grep -v '^#' .env | xargs)

# Run the application
./app
```

### Production Deployment

For production, use a systemd service:

1. **Create service file**
   ```bash
   sudo nano /etc/systemd/system/trading-bot.service
   ```

2. **Add configuration**
   ```ini
   [Unit]
   Description=Go Telegram Trading Bot
   After=network.target

   [Service]
   User=appuser
   Group=www-data
   WorkingDirectory=/path/to/Go-Telegram-Trading-Bot
   ExecStart=/path/to/Go-Telegram-Trading-Bot/app
   Restart=on-failure
   Environment=SESSION_SECRET=your-session-secret
   Environment=CSRF_AUTH_KEY=your-csrf-auth-key
   Environment=ADMIN_PASSWORD_HASH=your-admin-password-hash

   [Install]
   WantedBy=multi-user.target
   ```

3. **Enable and start the service**
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable trading-bot.service
   sudo systemctl start trading-bot.service
   ```

## 🔧 Using the Application

### Admin Panel

1. Access the admin panel at `http://your-domain/admin/login`
2. Login with username `admin` and your configured password
3. Configure:
   - Telegram Bot Token
   - Telegram Chat ID
   - Binance API credentials
   - Trading parameters

### Telegram Bot Commands

- `/start` - Initialize the bot
- `/help` - Display available commands
- `/status` - Check bot status
- `/settings` - View current settings

## 🔒 Security Best Practices

1. **Always use HTTPS** in production
2. **Regularly rotate API keys** for Binance
3. **Keep dependencies updated** to patch security vulnerabilities
4. **Backup your database** regularly
5. **Monitor logs** for suspicious activity
6. **Use strong passwords** for admin access
7. **Restrict server access** using firewalls

## 🐛 Troubleshooting

### Common Issues

- **Bot not responding**: Verify Telegram token and internet connectivity
- **Admin panel inaccessible**: Check server status and firewall settings
- **Trading errors**: Validate Binance API keys and permissions
- **CSRF errors**: Ensure CSRF_AUTH_KEY is properly set

### Viewing Logs

```bash
# For systemd service
sudo journalctl -u trading-bot.service -f

# For manual execution
./app > app.log 2>&1
```

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](./LICENSE) file for details.

---

**Disclaimer**: This software is for educational purposes only. Trading cryptocurrencies involves significant risk. Use at your own risk.
