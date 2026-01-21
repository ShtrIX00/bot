package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"

	"TGBOT2/internal/config"
	"TGBOT2/internal/storage"
	"TGBOT2/internal/tg3"
)

func main() {
	_ = godotenv.Load()
	cfg := config.MustLoad()
	if strings.TrimSpace(cfg.AccessPassword) == "" {
		log.Fatal("ACCESS_PASSWORD is not set")
	}

	if cfg.Bot3Token == "" {
		log.Fatal("BOT3_TOKEN is not set")
	}

	db := storage.MustOpen(cfg.DBPath)
	defer db.Close()

	bot, err := tgbotapi.NewBotAPI(cfg.Bot3Token)
	if err != nil {
		log.Fatalf("failed to create bot3: %v", err)
	}
	bot.Debug = true
	log.Printf("bot3 authorized as @%s", bot.Self.UserName)
	go startDailyDeadlineReminderBot3(bot, db)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		// ✅ CALLBACKS (inline кнопки)
		if upd.CallbackQuery != nil {
			// существующее: навигаторская рассылка
			tg3.HandleBroadcastCallback(bot, db, cfg, upd.CallbackQuery)

			// ✅ новое: подтверждение/правка заявки в группе
			tg3.HandleApprovalCallback(bot, db, cfg, upd.CallbackQuery)
			continue
		}

		if upd.Message == nil {
			continue
		}
		m := upd.Message

		// /chatid чтобы узнавать id чатов/групп
		if m.IsCommand() && m.Command() == "chatid" {
			chatID := m.Chat.ID
			msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("chat_id = %d", chatID))
			_, _ = bot.Send(msg)
			continue
		}

		// ✅ новое: сообщения в группе подтверждения (ждём reply после "Правка")
		// Важно: это должно отрабатывать ДО continue по навигатору/личке.
		if m.Chat != nil && cfg.Bot3ApprovalChatID != 0 && m.Chat.ID == cfg.Bot3ApprovalChatID {
			tg3.HandleApprovalGroupMessage(bot, cfg, m)
			continue
		}

		// навигаторский чат
		if m.Chat != nil && m.Chat.ID == cfg.Bot3NavigatorChatID {
			tg3.HandleNavigatorBroadcast(bot, db, cfg, m) // ✅ /broadcast /start кнопка "📨 Рассылка"
			tg3.HandleSupportReply(bot, db, cfg, m)       // ✅ ответы навигатора пользователям
			continue
		}

		// пользователи (только личка)
		if m.Chat != nil && m.Chat.IsPrivate() {
			tg3.HandleUserMessage(bot, db, cfg, m) // ✅ обычные сообщения улетают навигатору, заявки — в approval chat
			continue
		}
	}
}

func startDailyDeadlineReminderBot3(bot *tgbotapi.BotAPI, db *sql.DB) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		loc = time.FixedZone("MSK", 3*60*60)
	}

	const text = "Уважаемые партнёры, через 15 минут заканчивается приём заявок"
	lastSentDate := ""

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().In(loc)
		if now.Hour() != 15 || now.Minute() != 35 {
			continue
		}

		today := now.Format("2006-01-02")
		if lastSentDate == today {
			continue
		}
		lastSentDate = today

		chatIDs, err := storage.ListAllowedNotBlockedUserChatIDs(db)
		if err != nil {
			log.Printf("reminder bot3: ListAllowedNotBlockedUserChatIDs error: %v", err)
			continue
		}

		for _, cid := range chatIDs {
			_, _ = bot.Send(tgbotapi.NewMessage(cid, text))
		}

		log.Printf("reminder bot3 sent to %d users", len(chatIDs))
	}
}
