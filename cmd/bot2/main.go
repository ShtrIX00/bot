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
	"TGBOT2/internal/tg2"
)

func main() {
	_ = godotenv.Load()
	cfg := config.MustLoad()
	if strings.TrimSpace(cfg.AccessPassword) == "" {
		log.Fatal("ACCESS_PASSWORD is not set")
	}
	db := storage.MustOpen("bot.db")
	defer db.Close()

	if cfg.Bot2Token == "" {
		log.Fatal("BOT2_TOKEN is not set")
	}

	bot, err := tgbotapi.NewBotAPI(cfg.Bot2Token)
	if err != nil {
		log.Fatalf("failed to create bot2: %v", err)
	}
	bot.Debug = true
	log.Printf("bot2 authorized as @%s", bot.Self.UserName)
	go startDailyDeadlineReminderBot2(bot, db)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		if upd.CallbackQuery != nil {
			tg2.HandleBroadcastCallback(bot, db, cfg, upd.CallbackQuery)
			continue
		}

		if upd.Message == nil {
			continue
		}
		m := upd.Message

		// /chatid
		if m.IsCommand() && m.Command() == "chatid" {
			chatID := m.Chat.ID
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("chat_id = %d", chatID)))
			continue
		}

		// бухгалтерская группа или чат навигатора
		if m.Chat != nil && (m.Chat.ID == cfg.Bot2AccountingChatID || m.Chat.ID == cfg.Bot2NavigatorChatID) {
			tg2.HandleNavigatorBroadcast(bot, db, cfg, m)
			tg2.HandleSupportReply(bot, db, cfg, m)
			continue
		}

		// личка — пользователи
		if m.Chat != nil && m.Chat.IsPrivate() {
			tg2.HandleUserMessage(bot, db, cfg, m)
			continue
		}
	}
}

func startDailyDeadlineReminderBot2(bot *tgbotapi.BotAPI, db *sql.DB) {
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

		chatIDs, err := storage.ListAllowedUserChatIDs(db)
		if err != nil {
			log.Printf("reminder bot2: ListAllowedUserChatIDs error: %v", err)
			continue
		}

		for _, cid := range chatIDs {
			_, _ = bot.Send(tgbotapi.NewMessage(cid, text))
		}

		log.Printf("reminder bot2 sent to %d users", len(chatIDs))
	}
}
