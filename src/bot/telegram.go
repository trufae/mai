package main

import (
	"log"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func runTelegram(config Config, wg *sync.WaitGroup) {
	defer wg.Done()

	bot, err := tgbotapi.NewBotAPI(config.Telegram.Token)
	if err != nil {
		log.Printf("Error creating bot: %v", err)
		return
	}

	log.Printf("Bot authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)
	mention := "@" + bot.Self.UserName

	for update := range updates {
		if update.Message == nil {
			continue
		}

		processTelegramMessage(bot, update.Message, config, mention)
	}
}

func processTelegramMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config Config, mention string) {
	command := extractTelegramCommand(message, mention)
	if command == "" {
		return
	}

	response, exitCode := executeCommand(config, command)

	logInteraction(config, buildTelegramLogEntry(message, command, response, exitCode))

	sendTelegramMessage(bot, message.Chat.ID, message.MessageID, response, config.Telegram.MaxLength, config.Telegram.SplitMessages)
}

func extractTelegramCommand(message *tgbotapi.Message, mention string) string {
	switch message.Chat.Type {
	case "private":
		return message.Text
	default:
		if strings.HasPrefix(message.Text, mention) {
			return strings.TrimSpace(strings.TrimPrefix(message.Text, mention))
		}
	}
	return ""
}

func buildTelegramLogEntry(message *tgbotapi.Message, command, response string, exitCode int) logEntry {
	entry := logEntry{
		Query:      command,
		Response:   response,
		ReturnCode: exitCode,
	}

	if message.From != nil {
		entry.UserID = message.From.ID
		entry.Username = message.From.UserName
		entry.FirstName = message.From.FirstName
		entry.LastName = message.From.LastName
	}

	if message.Chat != nil {
		entry.ChatID = message.Chat.ID
	}

	return entry
}

func sendTelegramMessage(bot *tgbotapi.BotAPI, chatID int64, replyToID int, text string, maxLength int, splitMessages bool) {
	if maxLength <= 0 {
		maxLength = 4096
	}

	runes := []rune(text)
	if !splitMessages {
		if len(runes) > maxLength {
			runes = runes[:maxLength]
		}
		msg := tgbotapi.NewMessage(chatID, string(runes))
		if replyToID != 0 {
			msg.ReplyToMessageID = replyToID
		}
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Error sending message: %v", err)
		}
		return
	}

	firstChunk := true
	for len(runes) > 0 {
		var chunk string
		if len(runes) <= maxLength {
			chunk = string(runes)
			runes = nil
		} else {
			chunk = string(runes[:maxLength])
			runes = runes[maxLength:]
		}

		msg := tgbotapi.NewMessage(chatID, chunk)
		if firstChunk && replyToID != 0 {
			msg.ReplyToMessageID = replyToID
			firstChunk = false
		}

		if _, err := bot.Send(msg); err != nil {
			log.Printf("Error sending message chunk: %v", err)
			return
		}
	}
}
