# Go Telegram Trading Bot To Binance

This is a Go application that integrates with Telegram to manage trading signals received from TradingView webhooks. The application includes:

- **Admin Panel**: A web interface for configuring the Telegram bot and application settings.
- **Telegram Bot**: Interacts with users, allowing them to edit entry prices, take profits (TPs), stop loss (SL), and confirm signals.
- **Webhook Endpoint**: Receives alerts from TradingView and processes them.

---

## Table of Contents
- [Features](#features)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [Building the Application](#building-the-application)
- [Running the Application](#running-the-application)
- [Using the Application](#using-the-application)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)
- [License](#license)

---

## Features
- Receive alerts from TradingView via webhook and send them to Telegram.
- Edit and confirm trading signals through a Telegram bot.
- Admin panel for configuring the Telegram bot and application settings.
- Secure session management and CSRF protection.

---

## Prerequisites

1. **Go** version 1.17 or later installed on your system.
2. **SQLite** installed (the application uses a SQLite database).
3. **Telegram Bot Token** obtained from BotFather on Telegram.
4. **Telegram Chat ID** where the bot will operate.
5. A domain name and basic knowledge of setting up an HTTPS server if you intend to host the admin panel and webhook endpoint publicly.

---

## Installation

### Clone the Repository
```bash
git clone git@github.com:ayroop/Go-Telegram-Trading-Bot.git
cd Go-Telegram-Trading-Bot
```

### Install Dependencies
```bash
go mod tidy
go mod vendor
```

---

## Configuration

### Environment Variables
The application requires certain environment variables to be set before running:

- **SESSION_SECRET**: A strong, random secret used for session management.
- **CSRF_AUTH_KEY**: A 64-character hexadecimal string representing 32 bytes used for CSRF protection.
- **ADMIN_PASSWORD_HASH**: The bcrypt hash of the admin password.

### Generating Environment Variable Values

#### Generate `SESSION_SECRET`
```bash
openssl rand -hex 32
```
Copy the output (a 64-character hexadecimal string).

#### Generate `CSRF_AUTH_KEY`
```bash
openssl rand -hex 32
```
Copy the output (another 64-character hexadecimal string).

#### Generate `ADMIN_PASSWORD_HASH`
Create a Go script `generate_hash.go` to generate the bcrypt hash of your admin password:

```go
// generate_hash.go
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

Run the script:
```bash
go mod init hash_generator
go get golang.org/x/crypto/bcrypt
go run generate_hash.go
```
Copy the output (the bcrypt hash of your admin password).

---

### Using a `.env` File
You can use a `.env` file to store environment variables during development.

#### Create a `.env` File
```bash
touch .env
```

#### Add Environment Variables to `.env`
```env
SESSION_SECRET=your-session-secret
CSRF_AUTH_KEY=your-csrf-auth-key
ADMIN_PASSWORD_HASH=your-admin-password-hash
```
Replace `your-session-secret`, `your-csrf-auth-key`, and `your-admin-password-hash` with the values you generated earlier.

#### Load Environment Variables
When running the application manually, you can load the environment variables with:
```bash
export $(grep -v '^#' .env | xargs)
```
> **Note**: For production, do not load environment variables this way. Instead, use your system's environment variable mechanism or set them in the systemd service file, as described next.

---

### Setting Environment Variables in `app.service`
For running the application as a service, you can set environment variables directly in the systemd service file.

#### Create the systemd Service File
```bash
sudo nano /etc/systemd/system/app.service
```

Add the following content:
```ini
[Unit]
Description=Go Trading Bot Service
After=network.target

[Service]
User=appuser
Group=www-data
WorkingDirectory=/home/appuser/Go-Telegram-Trading-Bot
ExecStart=/home/appuser/Go-Telegram-Trading-Bot/app
Restart=on-failure
Environment=SESSION_SECRET=your-session-secret
Environment=CSRF_AUTH_KEY=your-csrf-auth-key
Environment=ADMIN_PASSWORD_HASH=your-admin-password-hash

[Install]
WantedBy=multi-user.target
```
Replace `your-session-secret`, `your-csrf-auth-key`, and `your-admin-password-hash` with the values you generated earlier.

#### Reload systemd and Enable the Service
```bash
sudo systemctl daemon-reload
sudo systemctl enable app.service
```

---

## Building the Application

Build the application by running:
```bash
go build -o app
```
This will create an executable named `app` in the current directory.

---

## Running the Application

### Using systemd Service
#### Start the Service
```bash
sudo systemctl start app.service
```

#### Check the Service Status
```bash
sudo systemctl status app.service
```
You should see that the service is active and running.

#### View the Logs
```bash
sudo journalctl -u app.service -f
```
This will display real-time logs from the application.

---

## Using the Application

### Accessing the Admin Panel
Navigate to:
```
http://your-domain/admin/login
```
Replace `your-domain` with your actual domain or `localhost` if running locally.

#### Login Credentials:
- **Username**: `admin`
- **Password**: The password you used when generating the `ADMIN_PASSWORD_HASH`.

### Configuring the Telegram Bot
After logging in:
1. Navigate to the **Configuration Page**. This should be `/admin/config`.
2. Enter the **Telegram Bot Token**. Obtain the bot token from BotFather on Telegram.
3. Enter the **Telegram Chat ID**. You can get your chat ID by messaging `@userinfobot` or other methods.
4. Save the Configuration. After saving, the application should initialize the Telegram bot.

---

## Best Practices

1. **Security**: Keep your `SESSION_SECRET`, `CSRF_AUTH_KEY`, and `ADMIN_PASSWORD_HASH` secure. Do not share them or commit them to version control.
2. **HTTPS**: Use HTTPS to serve your admin panel and webhook endpoint.
3. **Regular Updates**: Keep the application and dependencies up to date.
4. **Monitoring and Logging**: Regularly monitor logs for errors or suspicious activity.
5. **Backups**: Backup your database and important configuration.
6. **Immutable Secrets**: Do not store secrets in the code or in the database. Use environment variables or a secrets management system.
7. **Use `.gitignore`**: Ensure your `.gitignore` file excludes sensitive files like `.env`, `config.db`, and other secrets.
