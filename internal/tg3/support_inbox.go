package tg3

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"TGBOT2/internal/config"
	"TGBOT2/internal/storage"
)

// HandleSupportReply — ответы из чата навигатора bot3 пользователю.
// В чате навигатора надо отвечать reply на header или на forward пользователя.
func HandleSupportReply(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, m *tgbotapi.Message) {
	if m == nil || m.Chat == nil || m.From == nil {
		return
	}

	// только чат навигатора bot3
	if m.Chat.ID != cfg.Bot3NavigatorChatID {
		return
	}

	// только reply
	if m.ReplyToMessage == nil {
		return
	}

	// только разрешённые отвечающие
	if !cfg.ResponderIDs[int64(m.From.ID)] {
		return
	}

	// ищем, кому надо ответить
	target, ok, err := storage.GetReplyTarget(db, m.Chat.ID, m.ReplyToMessage.MessageID)
	if err != nil {
		log.Printf("tg3 GetReplyTarget error: %v", err)
		return
	}
	if !ok || target == nil {
		// молча — чтобы не шуметь в группе
		return
	}

	alias := ResponderAlias(cfg, m.From)
	txt := strings.TrimSpace(m.Text)

	// 1) ответ текстом
	if txt != "" && m.Document == nil && len(m.Photo) == 0 {
		out := tgbotapi.NewMessage(target.UserChatID, fmt.Sprintf("%s:\n%s", alias, txt))

		// если в чате навигатора отвечали reply на файл/фото — у пользователя делаем reply
		if m.ReplyToMessage.Document != nil || len(m.ReplyToMessage.Photo) > 0 {
			out.ReplyToMessageID = target.UserMessageID
		}

		if _, err := bot.Send(out); err != nil {
			log.Printf("tg3 send text to user error: %v", err)
		}
		return
	}

	// 2) ответ документом
	if m.Document != nil {
		doc := tgbotapi.NewDocument(target.UserChatID, tgbotapi.FileID(m.Document.FileID))

		a := strings.TrimSpace(strings.TrimSuffix(alias, ":"))
		capText := strings.TrimSpace(m.Caption)

		if capText == "" {
			doc.Caption = a
		} else {
			doc.Caption = fmt.Sprintf("%s:\n%s", a, capText)
		}

		doc.ReplyToMessageID = target.UserMessageID // reply оставляем
		if _, err := bot.Send(doc); err != nil {
			log.Printf("send doc to user error: %v", err)
		}
		return
	}

	if len(m.Photo) > 0 {
		ph := m.Photo[len(m.Photo)-1]
		p := tgbotapi.NewPhoto(target.UserChatID, tgbotapi.FileID(ph.FileID))

		a := strings.TrimSpace(strings.TrimSuffix(alias, ":"))
		capText := strings.TrimSpace(m.Caption)

		if capText == "" {
			p.Caption = a
		} else {
			p.Caption = fmt.Sprintf("%s:\n%s", a, capText)
		}

		p.ReplyToMessageID = target.UserMessageID
		if _, err := bot.Send(p); err != nil {
			log.Printf("send photo to user error: %v", err)
		}
		return
	}
}
