package tg3

import (
	"database/sql"
	"strconv"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"TGBOT2/internal/config"
	"TGBOT2/internal/storage"
)

type approvalItem struct {
	UserChatID    int64
	UserMessageID int
	Text          string

	AwaitFix bool
}

var (
	approvalMu   sync.Mutex
	approvalByID = map[int]*approvalItem{} // approval message_id -> item
)

func SendToApproval(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, userChatID int64, userMessageID int, text string) {
	if cfg.Bot3ApprovalChatID == 0 {
		// нет группы — просто молча не отправляем (или можно логировать)
		return
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Подтвердить", "app_ok"),
			tgbotapi.NewInlineKeyboardButtonData("✍️ Правка", "app_fix"),
		),
	)

	msg := tgbotapi.NewMessage(cfg.Bot3ApprovalChatID, text)
	msg.ReplyMarkup = kb
	sent, err := bot.Send(msg)
	if err != nil {
		return
	}

	// маппинг: чтобы можно было reply-цепочки поддерживать, если надо
	_ = storage.AddMap(db, cfg.Bot3ApprovalChatID, sent.MessageID, userChatID, userMessageID)

	approvalMu.Lock()
	approvalByID[sent.MessageID] = &approvalItem{
		UserChatID:    userChatID,
		UserMessageID: userMessageID,
		Text:          text,
		AwaitFix:      false,
	}
	approvalMu.Unlock()
}

func HandleApprovalCallback(bot *tgbotapi.BotAPI, cfg *config.Config, cq *tgbotapi.CallbackQuery) {
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil {
		return
	}
	if cq.Message.Chat.ID != cfg.Bot3ApprovalChatID {
		return
	}

	_, _ = bot.Request(tgbotapi.NewCallback(cq.ID, ""))

	approvalMsgID := cq.Message.MessageID

	approvalMu.Lock()
	item := approvalByID[approvalMsgID]
	approvalMu.Unlock()
	if item == nil {
		return
	}

	switch cq.Data {
	case "app_ok":
		// отправить пользователю итог (позже будет файл)
		out := tgbotapi.NewMessage(item.UserChatID, item.Text)
		_, _ = bot.Send(out)

		// отметить в группе
		ack := tgbotapi.NewMessage(cfg.Bot3ApprovalChatID, "✅ Отправлено пользователю.")
		ack.ReplyToMessageID = approvalMsgID
		_, _ = bot.Send(ack)

		approvalMu.Lock()
		delete(approvalByID, approvalMsgID)
		approvalMu.Unlock()

	case "app_fix":
		approvalMu.Lock()
		item.AwaitFix = true
		approvalMu.Unlock()

		ack := tgbotapi.NewMessage(cfg.Bot3ApprovalChatID, "✍️ Ок. Напишите причину правок reply на это сообщение.")
		ack.ReplyToMessageID = approvalMsgID
		_, _ = bot.Send(ack)
	}
}

// Обрабатываем сообщения в группе подтверждения.
// Если кто-то сделал reply на заявку после "Правка" — отправляем текст пользователю.
func HandleApprovalGroupMessage(bot *tgbotapi.BotAPI, cfg *config.Config, m *tgbotapi.Message) {
	if m == nil || m.Chat == nil {
		return
	}
	if m.Chat.ID != cfg.Bot3ApprovalChatID {
		return
	}
	if m.ReplyToMessage == nil {
		return
	}

	targetID := m.ReplyToMessage.MessageID

	approvalMu.Lock()
	item := approvalByID[targetID]
	approvalMu.Unlock()
	if item == nil || !item.AwaitFix {
		return
	}

	reason := strings.TrimSpace(m.Text)
	if reason == "" {
		// если вдруг прислали не текст — можно игнорировать
		return
	}

	text := "Заявка не подтверждена. Причина:\n" + reason + "\n\nПожалуйста, составьте заявку ещё раз с правками."
	out := tgbotapi.NewMessage(item.UserChatID, text)
	_, _ = bot.Send(out)

	ack := tgbotapi.NewMessage(cfg.Bot3ApprovalChatID, "📨 Причина отправлена пользователю.")
	ack.ReplyToMessageID = targetID
	_, _ = bot.Send(ack)

	approvalMu.Lock()
	delete(approvalByID, targetID)
	approvalMu.Unlock()
}

// (необязательно) помощь на будущее, если захочешь делать callback data с id:
// сейчас не нужно, т.к. мы используем cq.Message.MessageID как ключ.
func parseInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
