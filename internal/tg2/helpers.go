package tg2

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
		return "unknown"
	}
	if a, ok := cfg.ResponderAliases[int64(from.ID)]; ok && strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	if strings.TrimSpace(from.UserName) != "" {
		return "@" + strings.TrimSpace(from.UserName)
	}
	return fmt.Sprintf("id:%d", from.ID)
}

func StartText() string {
	return `Привет! 👋
Я успешно связал Вас с командой поддержки.

Как только сотрудники увидят Ваше сообщение,
они обязательно Вам ответят.

Вы можете написать свой вопрос прямо сейчас.`
}
