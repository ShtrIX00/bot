package tg3

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"TGBOT2/internal/config"
	"TGBOT2/internal/storage"
)

type BroadcastStage string

const (
	bStageIdle               BroadcastStage = "idle"
	bStageAwaitTemplate      BroadcastStage = "await_template"
	bStageAwaitSchedule      BroadcastStage = "await_schedule"
	bStageAwaitBlock         BroadcastStage = "await_block"
	bStageAwaitUnblock       BroadcastStage = "await_unblock"
	bStageAwaitDirectTarget  BroadcastStage = "await_direct_target"
	bStageAwaitDirectMessage BroadcastStage = "await_direct_message"
)

type BroadcastPayload struct {
	Text           string
	DocumentFileID string
	PhotoFileID    string
}

type navBroadcastState struct {
	Stage   BroadcastStage
	Payload *BroadcastPayload

	DirectUserChatID int64
	DirectUserRef    string
}

var navState = navBroadcastState{
	Stage:   bStageIdle,
	Payload: nil,
}

// =====================
// Public handlers
// =====================

func HandleNavigatorBroadcast(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, m *tgbotapi.Message) {
	if m == nil || m.Chat == nil {
		return
	}
	if m.Chat.ID != cfg.Bot3NavigatorChatID {
		return
	}

	// /start
	if m.IsCommand() && m.Command() == "start" {
		sendNavigatorWelcome(bot, m.Chat.ID)
		return
	}

	// /broadcast
	if m.IsCommand() && m.Command() == "broadcast" {
		startBroadcastFlow(bot, m.Chat.ID)
		return
	}

	// ====== FSM: block/unblock ======
	if navState.Stage == bStageAwaitBlock {
		if m.From == nil || !cfg.ResponderIDs[int64(m.From.ID)] {
			return
		}
		handleBlockInput(bot, db, m)
		return
	}
	if navState.Stage == bStageAwaitUnblock {
		if m.From == nil || !cfg.ResponderIDs[int64(m.From.ID)] {
			return
		}
		handleUnblockInput(bot, db, m)
		return
	}

	// ====== FSM: direct message ======
	if navState.Stage == bStageAwaitDirectTarget {
		if m.From == nil || !cfg.ResponderIDs[int64(m.From.ID)] {
			return
		}
		handleDirectTargetInput(bot, db, cfg, m)
		return
	}
	if navState.Stage == bStageAwaitDirectMessage {
		if m.From == nil || !cfg.ResponderIDs[int64(m.From.ID)] {
			return
		}
		handleDirectMessageSend(bot, cfg, m)
		return
	}

	// ====== FSM: broadcast schedule time ======
	if navState.Stage == bStageAwaitSchedule && navState.Payload != nil {
		handleScheduleTimeInput(bot, db, cfg, m)
		return
	}

	// ====== FSM: broadcast template ======
	if navState.Stage == bStageAwaitTemplate {
		captureBroadcastTemplate(bot, m)
		return
	}

	// ====== Buttons (when idle) ======
	txt := strings.TrimSpace(m.Text)

	if txt == "📨 Рассылка" {
		startBroadcastFlow(bot, m.Chat.ID)
		return
	}

	if txt == "🚫 Блокировка" {
		if m.From == nil || !cfg.ResponderIDs[int64(m.From.ID)] {
			return
		}
		navState.Stage = bStageAwaitBlock
		navState.Payload = nil
		navState.DirectUserChatID = 0
		navState.DirectUserRef = ""

		msg := tgbotapi.NewMessage(m.Chat.ID, "Введите telegram id (число) или @username для блокировки.\nОтмена: «❌ Отмена».")
		msg.ReplyMarkup = directMsgKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	if txt == "✅ Разблокировать" {
		if m.From == nil || !cfg.ResponderIDs[int64(m.From.ID)] {
			return
		}
		navState.Stage = bStageAwaitUnblock
		navState.Payload = nil
		navState.DirectUserChatID = 0
		navState.DirectUserRef = ""

		msg := tgbotapi.NewMessage(m.Chat.ID, "Введите telegram id (число) или @username для разблокировки.\nОтмена: «❌ Отмена».")
		msg.ReplyMarkup = directMsgKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	if txt == "✉️ Написать" {
		if m.From == nil || !cfg.ResponderIDs[int64(m.From.ID)] {
			return
		}

		navState.Stage = bStageAwaitDirectTarget
		navState.Payload = nil
		navState.DirectUserChatID = 0
		navState.DirectUserRef = ""

		msg := tgbotapi.NewMessage(m.Chat.ID,
			"Введите telegram id (число) или @username пользователя (allowed=1 и не в бане).\nОтмена: «❌ Отмена».")
		msg.ReplyMarkup = directMsgKeyboard()
		_, _ = bot.Send(msg)
		return
	}
}

func HandleBroadcastCallback(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, cq *tgbotapi.CallbackQuery) {
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil {
		return
	}
	if cq.Message.Chat.ID != cfg.Bot3NavigatorChatID {
		return
	}

	_, _ = bot.Request(tgbotapi.NewCallback(cq.ID, ""))

	switch cq.Data {
	case "broadcast_send_now":
		if navState.Payload == nil {
			return
		}
		cnt := broadcastToAll(bot, db, cfg, navState.Payload)
		navState.Stage = bStageIdle
		navState.Payload = nil

		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, fmt.Sprintf("Рассылка отправлена %d пользователям.", cnt))
		msg.ReplyMarkup = navigatorMainKeyboard()
		_, _ = bot.Send(msg)

	case "broadcast_schedule":
		if navState.Payload == nil {
			return
		}
		navState.Stage = bStageAwaitSchedule

		text := "Введите дату и время отправки в формате `DD.MM.YYYY HH:MM` (по Москве).\nНапример: `05.12.2025 10:30`"
		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, text)
		msg.ParseMode = "Markdown"
		msg.ReplyMarkup = directMsgKeyboard()
		_, _ = bot.Send(msg)

	case "broadcast_cancel":
		navState.Stage = bStageIdle
		navState.Payload = nil

		msg := tgbotapi.NewMessage(cq.Message.Chat.ID, "Рассылка отменена.")
		msg.ReplyMarkup = navigatorMainKeyboard()
		_, _ = bot.Send(msg)
	}
}

// =====================
// Keyboards
// =====================

func navigatorMainKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📨 Рассылка"),
			tgbotapi.NewKeyboardButton("🚫 Блокировка"),
			tgbotapi.NewKeyboardButton("✅ Разблокировать"),
			tgbotapi.NewKeyboardButton("✉️ Написать"),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

func directMsgKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("❌ Отмена"),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

func sendNavigatorWelcome(bot *tgbotapi.BotAPI, chatID int64) {
	text := "Панель навигатора (bot3):\n\n" +
		"📨 Рассылка — отправка всем пользователям\n" +
		"🚫 Блокировка — бот полностью игнорирует пользователя\n" +
		"✅ Разблокировать — снять блокировку\n" +
		"✉️ Написать — написать конкретному пользователю"

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = navigatorMainKeyboard()
	_, _ = bot.Send(msg)
}

// =====================
// 🚫 Block / ✅ Unblock
// =====================

func handleBlockInput(bot *tgbotapi.BotAPI, db *sql.DB, m *tgbotapi.Message) {
	txt := strings.TrimSpace(m.Text)
	if txt == "" {
		return
	}

	if txt == "❌ Отмена" {
		navState.Stage = bStageIdle
		msg := tgbotapi.NewMessage(m.Chat.ID, "Отменено.")
		msg.ReplyMarkup = navigatorMainKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	var telegramID int64
	var ok bool
	var err error

	if strings.HasPrefix(txt, "@") {
		telegramID, ok, err = storage.GetTelegramIDByUsername(db, txt)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Ошибка поиска по @username."))
			return
		}
		if !ok {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Пользователь не найден в базе (он должен хотя бы раз написать боту)."))
			return
		}
	} else {
		id, perr := strconv.ParseInt(txt, 10, 64)
		if perr != nil || id <= 0 {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Неверный формат. Введите telegram id или @username."))
			return
		}
		telegramID = id
		if _, found, _ := storage.GetUserChatIDByTelegramID(db, telegramID); !found {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Пользователь с таким telegram id не найден в базе (он должен хотя бы раз написать боту)."))
			return
		}
	}

	if err := storage.SetUserBlockedByTelegramID(db, telegramID, true); err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось заблокировать (ошибка БД)."))
		return
	}

	navState.Stage = bStageIdle
	msg := tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Готово. Пользователь %d заблокирован (blocked=1).", telegramID))
	msg.ReplyMarkup = navigatorMainKeyboard()
	_, _ = bot.Send(msg)
}

func handleUnblockInput(bot *tgbotapi.BotAPI, db *sql.DB, m *tgbotapi.Message) {
	txt := strings.TrimSpace(m.Text)
	if txt == "" {
		return
	}

	if txt == "❌ Отмена" {
		navState.Stage = bStageIdle
		msg := tgbotapi.NewMessage(m.Chat.ID, "Отменено.")
		msg.ReplyMarkup = navigatorMainKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	var telegramID int64
	var ok bool
	var err error

	if strings.HasPrefix(txt, "@") {
		telegramID, ok, err = storage.GetTelegramIDByUsername(db, txt)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Ошибка поиска по @username."))
			return
		}
		if !ok {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Пользователь не найден в базе (он должен хотя бы раз написать боту)."))
			return
		}
	} else {
		id, perr := strconv.ParseInt(txt, 10, 64)
		if perr != nil || id <= 0 {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Неверный формат. Введите telegram id или @username."))
			return
		}
		telegramID = id
		if _, found, _ := storage.GetUserChatIDByTelegramID(db, telegramID); !found {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Пользователь с таким telegram id не найден в базе (он должен хотя бы раз написать боту)."))
			return
		}
	}

	if err := storage.SetUserBlockedByTelegramID(db, telegramID, false); err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось разблокировать (ошибка БД)."))
		return
	}

	navState.Stage = bStageIdle
	msg := tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Готово. Пользователь %d разблокирован (blocked=0).", telegramID))
	msg.ReplyMarkup = navigatorMainKeyboard()
	_, _ = bot.Send(msg)
}

// =====================
// ✉️ Direct message flow
// =====================

func handleDirectTargetInput(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, m *tgbotapi.Message) {
	txt := strings.TrimSpace(m.Text)
	if txt == "" {
		return
	}

	if txt == "❌ Отмена" {
		navState.Stage = bStageIdle
		navState.DirectUserChatID = 0
		navState.DirectUserRef = ""

		msg := tgbotapi.NewMessage(m.Chat.ID, "Отменено.")
		msg.ReplyMarkup = navigatorMainKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	var chatID int64
	var ok bool
	var err error

	if strings.HasPrefix(txt, "@") {
		chatID, ok, err = storage.GetEligibleUserChatIDByUsername(db, txt)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Ошибка поиска по @username."))
			return
		}
		if !ok {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Пользователь не найден/не подходит (нужен allowed=1 и blocked=0)."))
			return
		}
		navState.DirectUserRef = txt
	} else {
		id, perr := strconv.ParseInt(txt, 10, 64)
		if perr != nil || id <= 0 {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Неверный формат. Введите telegram id или @username."))
			return
		}
		chatID, ok, err = storage.GetEligibleUserChatIDByTelegramID(db, id)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Ошибка поиска по id."))
			return
		}
		if !ok {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Пользователь не найден/не подходит (нужен allowed=1 и blocked=0)."))
			return
		}
		navState.DirectUserRef = fmt.Sprintf("id:%d", id)
	}

	navState.DirectUserChatID = chatID
	navState.Stage = bStageAwaitDirectMessage

	msg := tgbotapi.NewMessage(m.Chat.ID, "Ок. Теперь отправьте сообщение/файл/фото для "+navState.DirectUserRef+".\nОтмена: «❌ Отмена».")
	msg.ReplyMarkup = directMsgKeyboard()
	_, _ = bot.Send(msg)

	_ = cfg
}

func handleDirectMessageSend(bot *tgbotapi.BotAPI, cfg *config.Config, m *tgbotapi.Message) {
	txt := strings.TrimSpace(m.Text)

	if txt == "❌ Отмена" {
		navState.Stage = bStageIdle
		navState.DirectUserChatID = 0
		navState.DirectUserRef = ""

		msg := tgbotapi.NewMessage(m.Chat.ID, "Отменено.")
		msg.ReplyMarkup = navigatorMainKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	targetChatID := navState.DirectUserChatID
	if targetChatID == 0 {
		navState.Stage = bStageIdle

		msg := tgbotapi.NewMessage(m.Chat.ID, "Цель не выбрана. Нажмите «✉️ Написать» заново.")
		msg.ReplyMarkup = navigatorMainKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	alias := ResponderAlias(cfg, m.From)
	prefix := strings.TrimSpace(alias) + ":\n"

	if m.Document == nil && len(m.Photo) == 0 {
		if strings.TrimSpace(txt) == "" {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Отправьте текст или файл/фото."))
			return
		}
		out := tgbotapi.NewMessage(targetChatID, prefix+txt)
		if _, err := bot.Send(out); err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось отправить пользователю."))
			return
		}

	} else if m.Document != nil {
		doc := tgbotapi.NewDocument(targetChatID, tgbotapi.FileID(m.Document.FileID))
		cap := strings.TrimSpace(m.Caption)
		if cap != "" {
			doc.Caption = prefix + cap
		} else {
			doc.Caption = strings.TrimSuffix(prefix, "\n")
		}
		if _, err := bot.Send(doc); err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось отправить документ пользователю."))
			return
		}

	} else if len(m.Photo) > 0 {
		ph := m.Photo[len(m.Photo)-1]
		p := tgbotapi.NewPhoto(targetChatID, tgbotapi.FileID(ph.FileID))
		cap := strings.TrimSpace(m.Caption)
		if cap != "" {
			p.Caption = prefix + cap
		} else {
			p.Caption = strings.TrimSuffix(prefix, "\n")
		}
		if _, err := bot.Send(p); err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось отправить фото пользователю."))
			return
		}
	}

	done := tgbotapi.NewMessage(m.Chat.ID, "Отправлено пользователю "+navState.DirectUserRef+".")
	done.ReplyMarkup = navigatorMainKeyboard()
	_, _ = bot.Send(done)

	navState.Stage = bStageIdle
	navState.DirectUserChatID = 0
	navState.DirectUserRef = ""
}

// =====================
// 📨 Broadcast flow
// =====================

func startBroadcastFlow(bot *tgbotapi.BotAPI, chatID int64) {
	navState.Stage = bStageAwaitTemplate
	navState.Payload = nil

	msg := tgbotapi.NewMessage(chatID, "Отправьте сообщение, которое нужно разослать всем пользователям.\nМожно прикрепить файл или фото.")
	msg.ReplyMarkup = directMsgKeyboard()
	_, _ = bot.Send(msg)
}

func captureBroadcastTemplate(bot *tgbotapi.BotAPI, m *tgbotapi.Message) {
	if strings.TrimSpace(m.Text) == "❌ Отмена" {
		navState.Stage = bStageIdle
		navState.Payload = nil

		msg := tgbotapi.NewMessage(m.Chat.ID, "Отменено.")
		msg.ReplyMarkup = navigatorMainKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	payload := &BroadcastPayload{}

	text := strings.TrimSpace(m.Text)
	if text == "" && strings.TrimSpace(m.Caption) != "" {
		text = strings.TrimSpace(m.Caption)
	}
	if text != "" {
		payload.Text = text
	}
	if m.Document != nil {
		payload.DocumentFileID = m.Document.FileID
	}
	if len(m.Photo) > 0 {
		ph := m.Photo[len(m.Photo)-1]
		payload.PhotoFileID = ph.FileID
	}

	if payload.Text == "" && payload.DocumentFileID == "" && payload.PhotoFileID == "" {
		_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Нужно отправить текст или файл/фото (или вместе)."))
		return
	}

	navState.Payload = payload
	navState.Stage = bStageIdle

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📤 Отправить сейчас", "broadcast_send_now"),
			tgbotapi.NewInlineKeyboardButtonData("⏰ Запланировать", "broadcast_schedule"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отменить", "broadcast_cancel"),
		),
	)

	msg := tgbotapi.NewMessage(m.Chat.ID, "Выберите действие с рассылкой:")
	msg.ReplyMarkup = kb
	_, _ = bot.Send(msg)
}

func handleScheduleTimeInput(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, m *tgbotapi.Message) {
	if strings.TrimSpace(m.Text) == "❌ Отмена" {
		navState.Stage = bStageIdle
		navState.Payload = nil

		msg := tgbotapi.NewMessage(m.Chat.ID, "Отменено.")
		msg.ReplyMarkup = navigatorMainKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	text := strings.TrimSpace(m.Text)
	if text == "" || navState.Payload == nil {
		return
	}

	layout := "02.01.2006 15:04"
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		loc = time.FixedZone("MSK", 3*60*60)
	}

	tm, err := time.ParseInLocation(layout, text, loc)
	if err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Неверный формат. Пример: 05.12.2025 10:30"))
		return
	}
	if !tm.After(time.Now().In(loc)) {
		_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Время уже прошло. Укажите будущее время."))
		return
	}

	payloadCopy := *navState.Payload
	when := tm

	navState.Stage = bStageIdle
	navState.Payload = nil

	go func() {
		delay := time.Until(when)
		if delay > 0 {
			time.Sleep(delay)
		}
		broadcastToAll(bot, db, cfg, &payloadCopy)
	}()

	msg := tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ок, отправлю рассылку %s.", when.Format("02.01.2006 15:04")))
	msg.ReplyMarkup = navigatorMainKeyboard()
	_, _ = bot.Send(msg)
}

func broadcastToAll(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, payload *BroadcastPayload) int {
	if payload == nil {
		return 0
	}

	chatIDs, err := storage.ListAllUserChatIDs(db)
	if err != nil {
		log.Printf("tg3 ListAllUserChatIDs error: %v", err)
		return 0
	}

	text := strings.TrimSpace(payload.Text)

	const captionLimit = 1024
	caption := text
	extraText := ""
	if len([]rune(caption)) > captionLimit {
		r := []rune(caption)
		caption = string(r[:captionLimit-3]) + "..."
		extraText = text
	}

	sentCount := 0
	for _, cid := range chatIDs {
		if cid == cfg.Bot3NavigatorChatID {
			continue
		}

		if payload.DocumentFileID != "" {
			doc := tgbotapi.NewDocument(cid, tgbotapi.FileID(payload.DocumentFileID))
			if caption != "" {
				doc.Caption = caption
			}
			if _, err := bot.Send(doc); err != nil {
				log.Printf("broadcast doc to %d error: %v", cid, err)
				continue
			}
			if extraText != "" {
				_, _ = bot.Send(tgbotapi.NewMessage(cid, extraText))
			}
			sentCount++
			continue
		}

		if payload.PhotoFileID != "" {
			ph := tgbotapi.NewPhoto(cid, tgbotapi.FileID(payload.PhotoFileID))
			if caption != "" {
				ph.Caption = caption
			}
			if _, err := bot.Send(ph); err != nil {
				log.Printf("broadcast photo to %d error: %v", cid, err)
				continue
			}
			if extraText != "" {
				_, _ = bot.Send(tgbotapi.NewMessage(cid, extraText))
			}
			sentCount++
			continue
		}

		if text != "" {
			if _, err := bot.Send(tgbotapi.NewMessage(cid, text)); err != nil {
				log.Printf("broadcast text to %d error: %v", cid, err)
				continue
			}
			sentCount++
		}
	}

	return sentCount
}
