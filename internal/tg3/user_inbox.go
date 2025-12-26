package tg3

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"TGBOT2/internal/config"
	"TGBOT2/internal/storage"
)

const (
	btnMakeApplication = "📝 Составить заявку"
	btnCancel          = "❌ Отмена"
	btnSupport         = "🆘 Поддержка"
	btnSkip            = "⏭ Пропуск"
	btnContinue        = "▶️ Продолжить"

	company1 = "Компания 1"
	company2 = "Компания 2"
	company3 = "Компания 3"
)

type appStage int

const (
	stageIdle appStage = iota
	stageChooseCompany
	stageAwaitINN
	stageAwaitLegalName
	stageAwaitAmount
	stageAwaitPurpose
	stageAwaitContract
	stageSupportQuestion
	stageAwaitContinue // пауза
)

type applicationDraft struct {
	Company   string
	INN       string
	LegalName string
	Amount    string
	Purpose   string
	Contract  string

	RusKPP     string
	RusName    string
	RusAddress string
	RusErr     string
}

type userAppState struct {
	Stage       appStage
	ReturnStage appStage
	Draft       applicationDraft
}

var (
	appMu     sync.Mutex
	appByUser = map[int64]*userAppState{} // telegram_id -> state
)

// ✅ метим сообщения, которые ушли в поддержку (для reply в ответе навигатора)
var (
	supportMu        sync.RWMutex
	supportQuestions = map[string]bool{} // key = "chatID:msgID"
)

func markSupportQuestion(chatID int64, msgID int) {
	supportMu.Lock()
	defer supportMu.Unlock()
	supportQuestions[fmt.Sprintf("%d:%d", chatID, msgID)] = true
}

func isSupportQuestion(chatID int64, msgID int) bool {
	supportMu.RLock()
	defer supportMu.RUnlock()
	return supportQuestions[fmt.Sprintf("%d:%d", chatID, msgID)]
}

// ---------- keyboards ----------

func mainMenuKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnMakeApplication),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

func stepControlKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnCancel),
			tgbotapi.NewKeyboardButton(btnSupport),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

func contractKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnSkip),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnCancel),
			tgbotapi.NewKeyboardButton(btnSupport),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

func companyPickerKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(company1),
			tgbotapi.NewKeyboardButton(company2),
			tgbotapi.NewKeyboardButton(company3),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnCancel),
			tgbotapi.NewKeyboardButton(btnSupport),
		),
	)
	kb.ResizeKeyboard = true
	kb.OneTimeKeyboard = true
	return kb
}

func continueKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnContinue),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnCancel),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

// ---------- state helpers ----------

func getOrCreateState(telegramID int64) *userAppState {
	appMu.Lock()
	defer appMu.Unlock()

	st := appByUser[telegramID]
	if st == nil {
		st = &userAppState{Stage: stageIdle}
		appByUser[telegramID] = st
	}
	return st
}

func clearState(telegramID int64) {
	appMu.Lock()
	defer appMu.Unlock()
	delete(appByUser, telegramID)
}

// ---------- prompts ----------

func promptForStage(bot *tgbotapi.BotAPI, chatID int64, st *userAppState) {
	switch st.Stage {
	case stageChooseCompany:
		msg := tgbotapi.NewMessage(chatID, "Выберите компанию:")
		msg.ReplyMarkup = companyPickerKeyboard()
		_, _ = bot.Send(msg)

	case stageAwaitINN:
		msg := tgbotapi.NewMessage(chatID, "Введите ИНН:")
		msg.ReplyMarkup = stepControlKeyboard()
		_, _ = bot.Send(msg)

	case stageAwaitLegalName:
		msg := tgbotapi.NewMessage(chatID, "Введите название юр. лица:")
		msg.ReplyMarkup = stepControlKeyboard()
		_, _ = bot.Send(msg)

	case stageAwaitAmount:
		msg := tgbotapi.NewMessage(chatID, "Введите сумму платежа:")
		msg.ReplyMarkup = stepControlKeyboard()
		_, _ = bot.Send(msg)

	case stageAwaitPurpose:
		msg := tgbotapi.NewMessage(chatID, "Введите назначение платежа:")
		msg.ReplyMarkup = stepControlKeyboard()
		_, _ = bot.Send(msg)

	case stageAwaitContract:
		msg := tgbotapi.NewMessage(chatID, "Введите номер договора:")
		msg.ReplyMarkup = contractKeyboard()
		_, _ = bot.Send(msg)
	}
}

// ---------- rusprofile fetch ----------

func fetchRusprofileHTML(inn string) (string, error) {
	q := url.QueryEscape(strings.TrimSpace(inn))
	u := "https://www.rusprofile.ru/search?query=" + q

	client := &http.Client{Timeout: 12 * time.Second}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; tg-bot/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---------- main handler ----------

func HandleUserMessage(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, m *tgbotapi.Message) {
	if m == nil || m.Chat == nil || m.From == nil {
		return
	}
	if !m.Chat.IsPrivate() {
		return
	}

	// отвечающие — игнор
	if cfg.ResponderIDs[int64(m.From.ID)] {
		if m.IsCommand() && m.Command() == "start" {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, StartText()))
		}
		return
	}

	_ = storage.UpsertUser(db, mkUser(m))

	blocked, err := storage.IsUserBlockedByTelegramID(db, int64(m.From.ID))
	if err != nil || blocked {
		return
	}

	allowed, err := storage.IsUserAllowedByTelegramID(db, int64(m.From.ID))
	if err != nil {
		return
	}

	// /start
	if m.IsCommand() && m.Command() == "start" {
		if allowed {
			msg := tgbotapi.NewMessage(m.Chat.ID, StartText())
			msg.ReplyMarkup = mainMenuKeyboard()
			_, _ = bot.Send(msg)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, StartText()))
		}
		return
	}

	// пароль
	if !allowed {
		txt := strings.TrimSpace(m.Text)
		if txt != "" && cfg.AccessPassword != "" && txt == cfg.AccessPassword {
			_ = storage.SetUserAllowedByTelegramID(db, int64(m.From.ID), true)
			msg := tgbotapi.NewMessage(m.Chat.ID, "Принято, можете писать нашей команде")
			msg.ReplyMarkup = mainMenuKeyboard()
			_, _ = bot.Send(msg)
		}
		return
	}

	txt := strings.TrimSpace(m.Text)
	st := getOrCreateState(int64(m.From.ID))

	// ✅ ПАУЗА: пользователь может свободно писать навигатору
	if st.Stage == stageAwaitContinue {
		if txt == btnCancel {
			clearState(int64(m.From.ID))
			msg := tgbotapi.NewMessage(m.Chat.ID, "Заявка отменена.")
			msg.ReplyMarkup = mainMenuKeyboard()
			_, _ = bot.Send(msg)
			return
		}
		if txt == btnContinue {
			st.Stage = st.ReturnStage
			st.ReturnStage = stageIdle
			promptForStage(bot, m.Chat.ID, st)
			return
		}

		// любое другое сообщение/файл/фото — отправляем навигатору, НЕ ругаемся
		if txt != "" || m.Document != nil || len(m.Photo) > 0 {
			header := "От: " + UserRef(m.From)
			sendHeaderAndMap(bot, db, cfg.Bot3NavigatorChatID, header, m.Chat.ID, m.MessageID)
			forwardAndMap(bot, db, cfg.Bot3NavigatorChatID, m.Chat.ID, m.MessageID, m.Chat.ID, m.MessageID)

			// ✅ помечаем как support, чтобы ответ пришёл reply (если навигатор ответит reply в своём чате)
			markSupportQuestion(m.Chat.ID, m.MessageID)
		}

		// ничего пользователю не пишем, чтобы не мешать диалогу
		return
	}

	// кнопки во время заявки
	if st.Stage != stageIdle {
		if txt == btnCancel {
			clearState(int64(m.From.ID))
			msg := tgbotapi.NewMessage(m.Chat.ID, "Заявка отменена.")
			msg.ReplyMarkup = mainMenuKeyboard()
			_, _ = bot.Send(msg)
			return
		}
		if txt == btnSupport {
			st.ReturnStage = st.Stage
			st.Stage = stageSupportQuestion

			msg := tgbotapi.NewMessage(m.Chat.ID, "Напишите свой вопрос:")
			msg.ReplyMarkup = stepControlKeyboard()
			_, _ = bot.Send(msg)
			return
		}
	}

	// старт заявки
	if st.Stage == stageIdle && txt == btnMakeApplication {
		st.Stage = stageChooseCompany
		st.Draft = applicationDraft{}
		promptForStage(bot, m.Chat.ID, st)
		return
	}

	// поддержка: отправили вопрос → ставим на паузу
	if st.Stage == stageSupportQuestion {
		if txt == "" && m.Document == nil && len(m.Photo) == 0 {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Напишите текст или отправьте файл/фото."))
			return
		}

		header := "От: " + UserRef(m.From)
		sendHeaderAndMap(bot, db, cfg.Bot3NavigatorChatID, header, m.Chat.ID, m.MessageID)
		forwardAndMap(bot, db, cfg.Bot3NavigatorChatID, m.Chat.ID, m.MessageID, m.Chat.ID, m.MessageID)

		// ✅ помечаем этот вопрос как support
		markSupportQuestion(m.Chat.ID, m.MessageID)

		// ✅ пауза
		st.Stage = stageAwaitContinue

		msg := tgbotapi.NewMessage(m.Chat.ID, "Вопрос отправлен. Заполнение заявки поставлено на паузу.\nНажмите «Продолжить», чтобы продолжить с того же шага.")
		msg.ReplyMarkup = continueKeyboard()
		_, _ = bot.Send(msg)
		return
	}

	// обычный режим вне заявки — как раньше: просто навигатору
	if st.Stage == stageIdle {
		header := "От: " + UserRef(m.From)
		sendHeaderAndMap(bot, db, cfg.Bot3NavigatorChatID, header, m.Chat.ID, m.MessageID)
		forwardAndMap(bot, db, cfg.Bot3NavigatorChatID, m.Chat.ID, m.MessageID, m.Chat.ID, m.MessageID)
		return
	}

	// ----- шаги заявки -----
	switch st.Stage {
	case stageChooseCompany:
		choice := strings.TrimSpace(txt)
		if choice != company1 && choice != company2 && choice != company3 {
			msg := tgbotapi.NewMessage(m.Chat.ID, "Пожалуйста, выберите компанию кнопкой снизу.")
			msg.ReplyMarkup = companyPickerKeyboard()
			_, _ = bot.Send(msg)
			return
		}

		st.Draft.Company = choice
		st.Stage = stageAwaitINN

		// убираем клаву выбора компании
		msg := tgbotapi.NewMessage(m.Chat.ID, "Введите ИНН:")
		msg.ReplyMarkup = stepControlKeyboard()
		_, _ = bot.Send(msg)
		return

	case stageAwaitINN:
		if txt == "" {
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		st.Draft.INN = txt

		htmlText, err := fetchRusprofileHTML(txt)
		if err != nil {
			st.Draft.RusErr = "ошибка запроса rusprofile: " + err.Error()
		} else {
			data, perr := ParseRusprofileFromHTML(htmlText)
			if perr != nil {
				st.Draft.RusErr = perr.Error()
			} else if data != nil {
				st.Draft.RusKPP = data.KPP
				st.Draft.RusName = data.Name
				st.Draft.RusAddress = data.Address
			}
		}

		st.Stage = stageAwaitLegalName
		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAwaitLegalName:
		if txt == "" {
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		st.Draft.LegalName = txt
		st.Stage = stageAwaitAmount
		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAwaitAmount:
		if txt == "" {
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		st.Draft.Amount = txt
		st.Stage = stageAwaitPurpose
		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAwaitPurpose:
		if txt == "" {
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		st.Draft.Purpose = txt
		st.Stage = stageAwaitContract
		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAwaitContract:
		if txt == btnSkip {
			st.Draft.Contract = "0"
			sendForApproval(bot, db, cfg, m, st)
			return
		}
		if txt == "" {
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		st.Draft.Contract = txt
		sendForApproval(bot, db, cfg, m, st)
		return
	}
}

func sendForApproval(bot *tgbotapi.BotAPI, db *sql.DB, cfg *config.Config, m *tgbotapi.Message, st *userAppState) {
	user := UserRef(m.From)

	parts := []string{
		"📝 Заявка на подтверждение",
		fmt.Sprintf("От: %s", user),
		fmt.Sprintf("Компания: %s", st.Draft.Company),
		fmt.Sprintf("ИНН: %s", st.Draft.INN),
		fmt.Sprintf("Юр.лицо (ввод): %s", st.Draft.LegalName),
		fmt.Sprintf("Сумма: %s", st.Draft.Amount),
		fmt.Sprintf("Назначение: %s", st.Draft.Purpose),
		fmt.Sprintf("Договор: %s", st.Draft.Contract),
		"",
		"Данные Rusprofile:",
		fmt.Sprintf("КПП: %s", nz(st.Draft.RusKPP)),
		fmt.Sprintf("Название: %s", nz(st.Draft.RusName)),
		fmt.Sprintf("Адрес: %s", nz(st.Draft.RusAddress)),
	}
	if strings.TrimSpace(st.Draft.RusErr) != "" {
		parts = append(parts, "", "⚠️ Ошибка парсинга/получения:", st.Draft.RusErr)
	}

	text := strings.Join(parts, "\n")

	SendToApproval(bot, db, cfg, m.Chat.ID, m.MessageID, text)

	msg := tgbotapi.NewMessage(m.Chat.ID, "Заявка отправлена на подтверждение ✅")
	msg.ReplyMarkup = mainMenuKeyboard()
	_, _ = bot.Send(msg)

	clearState(int64(m.From.ID))
}

func nz(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return strings.TrimSpace(s)
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
		return
	}
	_ = storage.AddMap(db, dstChatID, sent.MessageID, userChatID, userMessageID)
}

func forwardAndMap(bot *tgbotapi.BotAPI, db *sql.DB, dstChatID int64, srcChatID int64, srcMsgID int, userChatID int64, userMessageID int) {
	fwd := tgbotapi.NewForward(dstChatID, srcChatID, srcMsgID)
	sent, err := bot.Send(fwd)
	if err != nil {
		return
	}
	_ = storage.AddMap(db, dstChatID, sent.MessageID, userChatID, userMessageID)
}
