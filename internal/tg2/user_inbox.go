package tg2

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"TGBOT2/internal/config"
	"TGBOT2/internal/storage"
)

func HandleUserMessage(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, m *tgbotapi.Message) {
	if m == nil || m.Chat == nil || m.From == nil {
		return
	}
	if !m.Chat.IsPrivate() {
		return
	}

	// Если бухгалтер/навигатор пишет боту в ЛИЧКУ — игнорируем
	if cfg.ResponderIDs[int64(m.From.ID)] {
		if m.IsCommand() && m.Command() == "start" {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, StartText()))
		}
		return
	}

	_ = storage.UpsertUser(db, mkUser(m))

	allowed, err := storage.IsUserAllowedByTelegramID(db, int64(m.From.ID))
	if err != nil {
		log.Printf("IsUserAllowedByTelegramID error: %v", err)
		return
	}

	// /start
	if m.IsCommand() && m.Command() == "start" {
		if allowed {
			name := strings.TrimSpace(m.From.FirstName)
			if name == "" {
				if strings.TrimSpace(m.From.UserName) != "" {
					name = "@" + strings.TrimSpace(m.From.UserName)
				}
			}
			if name != "" {
				_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID,
					fmt.Sprintf("Привет, %s!\nУ меня уже есть вся необходимая информация для нашего общения.", name),
				))
			} else {
				_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID,
					"Привет!\nУ меня уже есть вся необходимая информация для нашего общения.",
				))
			}
			return
		}

		_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, StartText()))
		return
	}

	// пароль (молча)
	if !allowed {
		txt := strings.TrimSpace(m.Text)
		if txt != "" && cfg.AccessPassword != "" && txt == cfg.AccessPassword {
			_ = storage.SetUserAllowedByTelegramID(db, int64(m.From.ID), true)
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Принято, можете писать нашей команде"))
		}
		return
	}

	// Авторизован: шлём в 1 бухгалтерскую группу + навигатору
	header := "От: " + UserRef(m.From)

	// бухгалтерия bot2
	sendHeaderAndMap(bot, db, cfg.Bot2AccountingChatID, header, m.Chat.ID, m.MessageID)
	forwardAndMap(bot, db, cfg.Bot2AccountingChatID, m.Chat.ID, m.MessageID, m.Chat.ID, m.MessageID)

	// навигатор bot2
	sendHeaderAndMap(bot, db, cfg.Bot2NavigatorChatID, header, m.Chat.ID, m.MessageID)
	forwardAndMap(bot, db, cfg.Bot2NavigatorChatID, m.Chat.ID, m.MessageID, m.Chat.ID, m.MessageID)
}

func mkUser(m *tgbotapi.Message) *storage.User {
	u := &storage.User{
		TelegramID: int64(m.From.ID),
		ChatID:     m.Chat.ID,
	}
	username := strings.TrimSpace(m.From.UserName)
	first := strings.TrimSpace(m.From.FirstName)
	last := strings.TrimSpace(m.From.LastName)
	if username != "" {
		u.Username = &username
	}
	if first != "" {
		u.FirstName = &first
	}
	if last != "" {
		u.LastName = &last
	}
	return u
}

func sendHeaderAndMap(bot *tgbotapi.BotAPI, db *sql.DB, dstChatID int64, text string, userChatID int64, userMessageID int) {
	msg := tgbotapi.NewMessage(dstChatID, text)
	sent, err := bot.Send(msg)
	if err != nil {
		log.Printf("send header error dst=%d: %v", dstChatID, err)
		return
	}
	_ = storage.AddMap(db, dstChatID, sent.MessageID, userChatID, userMessageID)
}

func forwardAndMap(bot *tgbotapi.BotAPI, db *sql.DB, dstChatID int64, srcChatID int64, srcMsgID int, userChatID int64, userMessageID int) {
	fwd := tgbotapi.NewForward(dstChatID, srcChatID, srcMsgID)
	sent, err := bot.Send(fwd)
	if err != nil {
		log.Printf("forward error dst=%d: %v", dstChatID, err)
		return
	}
	_ = storage.AddMap(db, dstChatID, sent.MessageID, userChatID, userMessageID)
}
