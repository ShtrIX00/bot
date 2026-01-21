package tg3

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"TGBOT2/internal/config"
	"TGBOT2/internal/storage"
)

type approvalItem struct {
	UserChatID    int64
	UserMessageID int
	Text          string
	Draft         applicationDraft
	AwaitFix      bool

	InvoiceNo int64
	XlsxPath  string
}

var (
	approvalMu   sync.Mutex
	approvalByID = map[int]*approvalItem{} // approval message_id -> item
)

func SendApplicationToApproval(
	bot *tgbotapi.BotAPI,
	db *sql.DB,
	cfg *config.Config,
	userChatID int64,
	userMessageID int,
	text string,
	draft applicationDraft,
) {
	if cfg.Bot3ApprovalChatID == 0 {
		// если нет чата подтверждения — просто сообщим пользователю
		_, _ = bot.Send(tgbotapi.NewMessage(userChatID, "Заявка принята. (чат подтверждения не настроен)"))
		return
	}

	// 1) номер счёта (уникальный)
	invoiceNo, err := storage.NextInvoiceNumber(db)
	if err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(userChatID, "Не смог сформировать счёт: "+err.Error()))
		return
	}

	// 2) дата
	loc, lerr := time.LoadLocation("Europe/Moscow")
	if lerr != nil {
		loc = time.FixedZone("MSK", 3*60*60)
	}
	now := time.Now().In(loc)

	// 3) шаблон
	tpl := strings.TrimSpace(cfg.Bot3InvoiceTemplatePath)
	if tpl == "" {
		tpl = "assets/invoice_template.xlsx"
	}

	// 4) генерим xlsx
	xlsxPath, perr := FillInvoiceTemplateXLSX(tpl, os.TempDir(), invoiceNo, now, draft, draft.Items)
	if perr != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(userChatID, "Не смог сформировать счёт: "+perr.Error()))
		return
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Подтвердить", "app_ok"),
			tgbotapi.NewInlineKeyboardButtonData("✍️ Правка", "app_fix"),
		),
	)

	// 5) в approval отправляем ФАЙЛ (не текст)
	doc := tgbotapi.NewDocument(cfg.Bot3ApprovalChatID, tgbotapi.FilePath(xlsxPath))
	doc.Caption = fmt.Sprintf("Счёт № %d (xlsx)\n\n%s", invoiceNo, text)
	doc.ReplyMarkup = kb

	sent, sendErr := bot.Send(doc)
	if sendErr != nil {
		// ВАЖНО: показываем ошибку прямо в approval-чате
		_, _ = bot.Send(tgbotapi.NewMessage(cfg.Bot3ApprovalChatID, "❌ Не смог отправить XLSX в этот чат: "+sendErr.Error()))
		// И на всякий случай уведомим пользователя
		_, _ = bot.Send(tgbotapi.NewMessage(userChatID, "Не смог отправить счёт на подтверждение."))
		return
	}

	// как в старом варианте — маппинг reply цепочек
	_ = storage.AddMap(db, cfg.Bot3ApprovalChatID, sent.MessageID, userChatID, userMessageID)

	// 6) дополнительно — навигатору тоже ФАЙЛ (если задан)
	if cfg.Bot3NavigatorChatID != 0 {
		navDoc := tgbotapi.NewDocument(cfg.Bot3NavigatorChatID, tgbotapi.FilePath(xlsxPath))
		navDoc.Caption = fmt.Sprintf("Счёт № %d (xlsx)\n\n%s", invoiceNo, text)
		if _, err := bot.Send(navDoc); err != nil {
			// не критично, но пусть будет видно
			_, _ = bot.Send(tgbotapi.NewMessage(cfg.Bot3ApprovalChatID, "⚠️ Не смог отправить XLSX навигатору: "+err.Error()))
		}
	}

	approvalMu.Lock()
	approvalByID[sent.MessageID] = &approvalItem{
		UserChatID:    userChatID,
		UserMessageID: userMessageID,
		Text:          text,
		Draft:         draft,
		AwaitFix:      false,
		InvoiceNo:     invoiceNo,
		XlsxPath:      xlsxPath,
	}
	approvalMu.Unlock()
}

func HandleApprovalCallback(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, cq *tgbotapi.CallbackQuery) {
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
		if item.XlsxPath == "" {
			_, _ = bot.Send(tgbotapi.NewMessage(item.UserChatID, "Не найден файл счёта для отправки."))
			return
		}

		doc := tgbotapi.NewDocument(item.UserChatID, tgbotapi.FilePath(item.XlsxPath))
		doc.Caption = "Счёт на оплату № " + strconv.FormatInt(item.InvoiceNo, 10)
		_, _ = bot.Send(doc)

		ack := tgbotapi.NewMessage(cfg.Bot3ApprovalChatID, "✅ Счёт отправлен пользователю.")
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
		return
	}

	out := tgbotapi.NewMessage(item.UserChatID, "Заявка не подтверждена. Причина:\n"+reason+"\n\nСоставьте заявку заново с правками.")
	_, _ = bot.Send(out)

	ack := tgbotapi.NewMessage(cfg.Bot3ApprovalChatID, "📨 Причина отправлена пользователю.")
	ack.ReplyToMessageID = targetID
	_, _ = bot.Send(ack)

	approvalMu.Lock()
	delete(approvalByID, targetID)
	approvalMu.Unlock()
}
