package tg

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
			SendCompanyPicker(bot, m.Chat.ID)
			return
		}

		_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, StartText()))
		return
	}

	// Если не авторизован — ждём пароль МОЛЧА
	if !allowed {
		txt := strings.TrimSpace(m.Text)
		if txt != "" && cfg.AccessPassword != "" && txt == cfg.AccessPassword {
			_ = storage.SetUserAllowedByTelegramID(db, int64(m.From.ID), true)
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Принято, можете писать нашей команде"))
			SendCompanyPicker(bot, m.Chat.ID)
		}
		return
	}

	// Авторизован: обработка выбора компании
	if company, ok := TryParseCompanyChoice(m.Text); ok {
		SaveCompanyChoice(bot, db, m.Chat.ID, int64(m.From.ID), company)
		return
	}

	// ✅ пробуем взять из БД
	company, err := storage.GetUserCompanyByTelegramID(db, int64(m.From.ID))
	if err != nil {
		log.Printf("GetUserCompanyByTelegramID error: %v", err)
		company = 0
	}

	// ✅ если БД вернула 0 — берём из кэша (фикс твоей проблемы)
	if company == 0 {
		company = getCachedCompany(int64(m.From.ID))
	}

	if company == 0 {
		SendCompanyPicker(bot, m.Chat.ID)
		return
	}

	accChatID := AccountingChatIDByCompany(cfg, company)
	if accChatID == 0 {
		// тут отдельное сообщение, чтобы было видно, что проблема в env/chat_id, а не в выборе компании
		_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Ошибка: не настроен chat_id бухгалтерии для выбранной компании."))
		SendCompanyPicker(bot, m.Chat.ID)
		return
	}

	// --- Заголовки ---
	userHeader := "От: " + UserRef(m.From)

	// бухгалтерии
	sendHeaderAndMap(bot, db, accChatID, userHeader, m.Chat.ID, m.MessageID)
	forwardAndMap(bot, db, accChatID, m.Chat.ID, m.MessageID, m.Chat.ID, m.MessageID)

	// навигатору
	navHeader := fmt.Sprintf("%s\nКому: %s", userHeader, CompanyName(company))
	sendHeaderAndMap(bot, db, cfg.Bot1NavigatorChatID, navHeader, m.Chat.ID, m.MessageID)
	forwardAndMap(bot, db, cfg.Bot1NavigatorChatID, m.Chat.ID, m.MessageID, m.Chat.ID, m.MessageID)
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
