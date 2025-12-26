package tg

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"TGBOT2/internal/config"
)

func UserRef(u *tgbotapi.User) string {
	if u == nil {
		return "id:unknown"
	}
	if strings.TrimSpace(u.UserName) != "" {
		return "@" + strings.TrimSpace(u.UserName)
	}
	return fmt.Sprintf("id:%d", u.ID)
}

func ResponderAlias(cfg *config.Config, from *tgbotapi.User) string {
	if from == nil {
		return "unknown:"
	}

	alias := ""
	if a, ok := cfg.ResponderAliases[int64(from.ID)]; ok && strings.TrimSpace(a) != "" {
		alias = strings.TrimSpace(a)
	} else if strings.TrimSpace(from.UserName) != "" {
		alias = "@" + strings.TrimSpace(from.UserName)
	} else {
		alias = fmt.Sprintf("id:%d", from.ID)
	}

	// <-- вот это и делает "buh111:" вместо "buh111"
	return alias
}
func CompanyKeyboard() tgbotapi.ReplyKeyboardMarkup {
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
	return kb
}

func StartText() string {
	return `Привет! 👋
Я успешно связал Вас с командой поддержки.

Как только сотрудники увидят Ваше сообщение,
они обязательно Вам ответят.

Вы можете написать свой вопрос прямо сейчас.`
}
