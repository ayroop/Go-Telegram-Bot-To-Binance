Go Telegram Trading Bot To Binance
This is a Go application that integrates with Telegram to manage trading signals received from TradingView webhooks. The application includes:

Admin Panel: A web interface for configuring the Telegram bot and application settings.
Telegram Bot: Interacts with users, allowing them to edit entry prices, take profits (TPs), stop loss (SL), and confirm signals.
Webhook Endpoint: Receives alerts from TradingView and processes them.
Table of Contents
Features
Prerequisites
Installation
Configuration
Environment Variables
Generating Environment Variable Values
Using a .env File
Setting Environment Variables in app.service
Building the Application
Running the Application
Using systemd Service
Using the Application
Accessing the Admin Panel
Configuring the Telegram Bot
Best Practices
Troubleshooting
License
Features
Receive alerts from TradingView via webhook and send them to Telegram.
Edit and confirm trading signals through a Telegram bot.
Admin panel for configuring the Telegram bot and application settings.
Secure session management and CSRF protection.
Prerequisites
Go version 1.17 or later installed on your system.
SQLite installed (the application uses a SQLite database).
Telegram Bot Token obtained from BotFather on Telegram.
Telegram Chat ID where the bot will operate.
A domain name and basic knowledge of setting up an HTTPS server if you intend to host the admin panel and webhook endpoint publicly.
Installation
Clone the Repository
git clone git@github.com:ayroop/Go-Telegram-Trading-Bot.git
cd Go-Telegram-Trading-Bot
Copy
Insert

Install Dependencies
go mod tidy
go mod vendor
Copy
Insert

Configuration
Environment Variables
The application requires certain environment variables to be set before running:

SESSION_SECRET: A strong, random secret used for session management.
CSRF_AUTH_KEY: A 64-character hexadecimal string representing 32 bytes used for CSRF protection.
ADMIN_PASSWORD_HASH: The bcrypt hash of the admin password.
Generating Environment Variable Values
Generate SESSION_SECRET
openssl rand -hex 32
Copy
Insert

Copy the output (a 64-character hexadecimal string).
Generate CSRF_AUTH_KEY
openssl rand -hex 32
Copy
Insert

Copy the output (another 64-character hexadecimal string).
Generate ADMIN_PASSWORD_HASH Create a Go script generate_hash.go to generate the bcrypt hash of your admin password:
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
Copy
Insert

Run the script:
go mod init hash_generator
go get golang.org/x/crypto/bcrypt
go run generate_hash.go
Copy
Insert

Copy the output (the bcrypt hash of your admin password).
Using a .env File
You can use a .env file to store environment variables during development.

Create a .env file
touch .env
Copy
Insert

Add Environment Variables to .env
SESSION_SECRET=your-session-secret
CSRF_AUTH_KEY=your-csrf-auth-key
ADMIN_PASSWORD_HASH=your-admin-password-hash
Copy
Insert

Replace your-session-secret, your-csrf-auth-key, and your-admin-password-hash with the values you generated earlier.
Load Environment Variables When running the application manually, you can load the environment variables with:
export $(grep -v '^#' .env | xargs)
Copy
Insert

Note: For production, do not load environment variables this way. Instead, use your system's environment variable mechanism or set them in the systemd service file, as described next.
Setting Environment Variables in app.service
For running the application as a service, you can set environment variables directly in the systemd service file.

Create the systemd Service File Create /etc/systemd/system/app.service:
sudo nano /etc/systemd/system/app.service
Copy
Insert

Add the following content:
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
Copy
Insert

Replace your-session-secret, your-csrf-auth-key, and your-admin-password-hash with the values you generated earlier.
Reload systemd and Enable the Service
sudo systemctl daemon-reload
sudo systemctl enable app.service
Copy
Insert

Building the Application
Build the application by running:

go build -o app
Copy
Insert

This will create an executable named app in the current directory.

Running the Application
Using systemd Service
Start the Service
sudo systemctl start app.service
Copy
Insert

Check the Service Status
sudo systemctl status app.service
Copy
Insert

You should see that the service is active and running.
View the Logs
sudo journalctl -u app.service -f
Copy
Insert

This will display real-time logs from the application.
Using the Application
Accessing the Admin Panel
Navigate to:

http://your-domain/admin/login
Copy
Insert

Replace your-domain with your actual domain or localhost if running locally.

Login Credentials:

Username: admin
Password: The password you used when generating the ADMIN_PASSWORD_HASH.
Configuring the Telegram Bot
After logging in:

Navigate to the Configuration Page This should be /admin/config.
Enter the Telegram Bot Token Obtain the bot token from BotFather on Telegram.
Enter the Telegram Chat ID You can get your chat ID by messaging @userinfobot or other methods.
Save the Configuration
After saving, the application should initialize the Telegram bot.

Best Practices
Security: Keep your SESSION_SECRET, CSRF_AUTH_KEY, and ADMIN_PASSWORD_HASH secure. Do not share them or commit them to version control.
HTTPS: Use HTTPS to serve your admin panel and webhook endpoint.
Regular Updates: Keep the application and dependencies up to date.
Monitoring and Logging: Regularly monitor logs for errors or suspicious activity.
Backups: Backup your database and important configuration.
Immutable Secrets: Do not store secrets in the code or in the database. Use environment variables or a secrets management system.
Use .gitignore: Ensure your .gitignore file excludes sensitive files like .env, config.db, and other secrets.
Troubleshooting
Application Won't Start
Check if all environment variables are set correctly.
Run sudo systemctl status app.service to check for errors.
View logs with sudo journalctl -u app.service -b.
Cannot Access Admin Panel
Ensure the application is running.
Check that your domain is pointing to your server.
Verify that your firewall allows incoming connections.
Telegram Bot Not Responding
Check if the Telegram bot token and chat ID are correct.
Ensure that the application is running without errors.
Verify network connectivity from your server to Telegram.
CSRF Token Errors
Ensure that the CSRF token is properly included in your form templates.
Verify that CSRF_AUTH_KEY is correctly set and is a 64-character hexadecimal string.
Check that the CSRF middleware is properly initialized and applied to handlers.
Port Conflicts
Ensure no other applications are using the same port.
When running the application manually, stop the systemd service to avoid port conflicts.
License
This project is licensed under the MIT License - see the LICENSE file for details.

Note: Ensure you replace placeholder values like your-session-secret, your-csrf-auth-key, your-admin-password-hash, and your-domain with your actual values.
