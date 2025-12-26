package tg

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"TGBOT2/internal/config"
	"TGBOT2/internal/storage"
)

var (
	companyMu    sync.RWMutex
	companyCache = map[int64]int{} // telegramUserID -> company
)

func setCachedCompany(telegramUserID int64, company int) {
	companyMu.Lock()
	defer companyMu.Unlock()
	companyCache[telegramUserID] = company
}

func getCachedCompany(telegramUserID int64) int {
	companyMu.RLock()
	defer companyMu.RUnlock()
	return companyCache[telegramUserID]
}

func companyReplyKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("Компания 1"),
			tgbotapi.NewKeyboardButton("Компания 2"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("Компания 3"),
			tgbotapi.NewKeyboardButton("Компания 4"),
		),
	)
	kb.ResizeKeyboard = true
	kb.OneTimeKeyboard = true
	return kb
}

func SendCompanyPicker(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "Перед отправкой документов выберите, по какой компании вы хотите их отправить")
	msg.ReplyMarkup = companyReplyKeyboard()
	_, _ = bot.Send(msg)
}

func TryParseCompanyChoice(text string) (int, bool) {
	t := strings.TrimSpace(strings.ToLower(text))
	switch t {
	case "компания 1":
		return 1, true
	case "компания 2":
		return 2, true
	case "компания 3":
		return 3, true
	case "компания 4":
		return 4, true
	default:
		return 0, false
	}
}

func SaveCompanyChoice(bot *tgbotapi.BotAPI, db *sql.DB, chatID int64, telegramUserID int64, company int) {
	// ✅ сохраняем в кэш ВСЕГДА
	setCachedCompany(telegramUserID, company)

	// ✅ пытаемся сохранить в БД и логируем ошибку (раньше ты её терял)
	if err := storage.SetUserCompanyByTelegramID(db, telegramUserID, company); err != nil {
		log.Printf("SetUserCompanyByTelegramID error: %v", err)
	}

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Выбрана: %s\nМожете отправлять файлы", CompanyName(company)))
	msg.ReplyMarkup = companyReplyKeyboard()
	_, _ = bot.Send(msg)
}

func AccountingChatIDByCompany(cfg *config.Config, company int) int64 {
	switch company {
	case 1:
		return cfg.Accounting1ChatID
	case 2:
		return cfg.Accounting2ChatID
	case 3:
		return cfg.Accounting3ChatID
	case 4:
		return cfg.Accounting4ChatID
	default:
		return 0
	}
}

func CompanyName(company int) string {
	switch company {
	case 1:
		return "Компания 1"
	case 2:
		return "Компания 2"
	case 3:
		return "Компания 3"
	case 4:
		return "Компания 4"
	default:
		return "Компания ?"
	}
}
