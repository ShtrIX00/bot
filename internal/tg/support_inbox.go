package tg

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"TGBOT2/internal/config"
	"TGBOT2/internal/storage"
)

func HandleSupportReply(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, m *tgbotapi.Message) {
	if m == nil || m.Chat == nil || m.From == nil {
		return
	}

	isAccounting :=
		m.Chat.ID == cfg.Accounting1ChatID ||
			m.Chat.ID == cfg.Accounting2ChatID ||
			m.Chat.ID == cfg.Accounting3ChatID ||
			m.Chat.ID == cfg.Accounting4ChatID

	isNavigator := m.Chat.ID == cfg.Bot1NavigatorChatID

	if !isAccounting && !isNavigator {
		return
	}
	if m.ReplyToMessage == nil {
		return
	}
	if !cfg.ResponderIDs[int64(m.From.ID)] {
		return
	}

	target, ok, err := storage.GetReplyTarget(db, m.Chat.ID, m.ReplyToMessage.MessageID)
	if err != nil {
		log.Printf("GetReplyTarget error: %v", err)
		return
	}
	if !ok || target == nil {
		return
	}

	alias := ResponderAlias(cfg, m.From)
	a := strings.TrimSpace(strings.TrimSuffix(alias, ":")) // для текста пользователю (без двоеточия)

	// ===== TEXT =====
	txt := strings.TrimSpace(m.Text)
	if txt != "" && m.Document == nil && len(m.Photo) == 0 {
		out := tgbotapi.NewMessage(target.UserChatID, fmt.Sprintf("%s:\n%s", a, txt))

		// если ответили на файл/фото пользователя — делаем reply у пользователя
		if m.ReplyToMessage != nil && (m.ReplyToMessage.Document != nil || len(m.ReplyToMessage.Photo) > 0) {
			out.ReplyToMessageID = target.UserMessageID
		}

		if _, err := bot.Send(out); err != nil {
			log.Printf("send text to user error: %v", err)
			return
		}

		// уведомление в бухгалтерию (текстом)
		if isNavigator {
			notifyAccountingNavigatorRepliedText(bot, db, cfg, target, a, txt)
		}
		return
	}

	// ===== DOCUMENT (важно: раньше photo) =====
	if m.Document != nil {
		doc := tgbotapi.NewDocument(target.UserChatID, tgbotapi.FileID(m.Document.FileID))

		capText := strings.TrimSpace(m.Caption)
		if capText == "" {
			doc.Caption = a
		} else {
			doc.Caption = fmt.Sprintf("%s:\n%s", a, capText)
		}

		doc.ReplyToMessageID = target.UserMessageID
		if _, err := bot.Send(doc); err != nil {
			log.Printf("send document to user error: %v", err)
			return
		}

		// ✅ уведомление в бухгалтерию: сам файл + подпись (reply на сообщение пользователя в группе)
		if isNavigator {
			notifyAccountingNavigatorRepliedMedia(bot, db, cfg, target, a, "document", m.Document.FileID, capText)
		}
		return
	}

	// ===== PHOTO =====
	if len(m.Photo) > 0 {
		ph := m.Photo[len(m.Photo)-1]
		p := tgbotapi.NewPhoto(target.UserChatID, tgbotapi.FileID(ph.FileID))

		capText := strings.TrimSpace(m.Caption)
		if capText == "" {
			p.Caption = a
		} else {
			p.Caption = fmt.Sprintf("%s:\n%s", a, capText)
		}

		p.ReplyToMessageID = target.UserMessageID
		if _, err := bot.Send(p); err != nil {
			log.Printf("send photo to user error: %v", err)
			return
		}

		// ✅ уведомление в бухгалтерию: само фото + подпись (reply на сообщение пользователя в группе)
		if isNavigator {
			notifyAccountingNavigatorRepliedMedia(bot, db, cfg, target, a, "photo", ph.FileID, capText)
		}
		return
	}
}

// ---- helpers for notify ----

func notifyAccountingNavigatorRepliedText(
	bot *tgbotapi.BotAPI,
	db *sql.DB,
	cfg *config.Config,
	target *storage.ReplyTarget,
	navAlias string,
	content string,
) {
	accChatID, groupMsgID, ok := findAccountingReplyAnchor(db, cfg, target)
	if !ok {
		return
	}

	summary := strings.TrimSpace(content)
	if summary == "" {
		summary = "[сообщение]"
	}
	// ограничим для читаемости
	r := []rune(summary)
	if len(r) > 300 {
		summary = string(r[:300]) + "…"
	}

	text := fmt.Sprintf("‼️%s ответил пользователю:\n%s", navAlias, summary)
	msg := tgbotapi.NewMessage(accChatID, text)
	msg.ReplyToMessageID = groupMsgID
	_, _ = bot.Send(msg)
}

func notifyAccountingNavigatorRepliedMedia(
	bot *tgbotapi.BotAPI,
	db *sql.DB,
	cfg *config.Config,
	target *storage.ReplyTarget,
	navAlias string,
	kind string, // "document" | "photo"
	fileID string,
	captionText string,
) {
	if strings.TrimSpace(fileID) == "" {
		// если почему-то нет file_id — fallback на текстовое уведомление
		notifyAccountingNavigatorRepliedText(bot, db, cfg, target, navAlias, captionText)
		return
	}

	accChatID, groupMsgID, ok := findAccountingReplyAnchor(db, cfg, target)
	if !ok {
		return
	}

	cap := buildMediaCaption(navAlias, captionText, kind)

	switch kind {
	case "document":
		doc := tgbotapi.NewDocument(accChatID, tgbotapi.FileID(fileID))
		if cap != "" {
			doc.Caption = cap
		}
		doc.ReplyToMessageID = groupMsgID
		_, _ = bot.Send(doc)
	case "photo":
		ph := tgbotapi.NewPhoto(accChatID, tgbotapi.FileID(fileID))
		if cap != "" {
			ph.Caption = cap
		}
		ph.ReplyToMessageID = groupMsgID
		_, _ = bot.Send(ph)
	default:
		// fallback
		notifyAccountingNavigatorRepliedText(bot, db, cfg, target, navAlias, captionText)
	}
}

func findAccountingReplyAnchor(db *sql.DB, cfg *config.Config, target *storage.ReplyTarget) (accChatID int64, groupMsgID int, ok bool) {
	accChats := []int64{
		cfg.Accounting1ChatID,
		cfg.Accounting2ChatID,
		cfg.Accounting3ChatID,
		cfg.Accounting4ChatID,
	}

	accChatID, ok2, err := storage.FindMappedChatForUserMessage(db, target.UserChatID, target.UserMessageID, accChats)
	if err != nil {
		log.Printf("notifyAccounting: FindMappedChatForUserMessage error: %v", err)
		return 0, 0, false
	}
	if !ok2 || accChatID == 0 {
		return 0, 0, false
	}

	groupMsgID, ok3, err := storage.GetMappedGroupMessageID(db, accChatID, target.UserChatID, target.UserMessageID)
	if err != nil {
		log.Printf("notifyAccounting: GetMappedGroupMessageID error: %v", err)
		return 0, 0, false
	}
	if !ok3 || groupMsgID == 0 {
		return 0, 0, false
	}

	return accChatID, groupMsgID, true
}

func buildMediaCaption(navAlias string, captionText string, kind string) string {
	alias := strings.TrimSpace(navAlias)
	ct := strings.TrimSpace(captionText)

	summary := ct
	if summary == "" {
		switch kind {
		case "document":
			summary = "[документ]"
		case "photo":
			summary = "[фото]"
		default:
			summary = "[сообщение]"
		}
	}

	out := fmt.Sprintf("‼️%s ответил пользователю:\n%s", alias, summary)

	// Telegram caption limit ~1024
	const max = 1024
	if utf8.RuneCountInString(out) <= max {
		return out
	}
	r := []rune(out)
	if len(r) <= max {
		return out
	}
	return string(r[:max-1]) + "…"
}
