package tg3

import (
	"database/sql"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
	btnAddItem         = "➕ Добавить позицию"
	btnFinishItems     = "✅ Готово"

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
	stageAwaitItemName
	stageAwaitItemQty
	stageAwaitItemUnit
	stageAwaitItemUnitPrice
	stageAwaitItemLineTotal
	stageAskMoreItems
	stageAwaitContract
	stageSupportQuestion
	stageAwaitContinue // пауза
)

type applicationDraft struct {
	Company   string
	INN       string
	LegalName string
	Contract  string

	Items []appItem
	// суммарно по позициям (заполняем перед отправкой в approval)
	TotalSum float64

	RusKPP     string
	RusName    string
	RusAddress string
	RusErr     string
}

type userAppState struct {
	Stage       appStage
	ReturnStage appStage
	Draft       applicationDraft
	// временно храним текущую позицию пока пользователь заполняет шаги
	CurItem appItem
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

var reOrgClean = regexp.MustCompile(`[^\pL\pN]+`)

func normalizeOrgName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))

	// частые формы — убираем, чтобы не мешали сравнению
	repl := []string{
		"общество с ограниченной ответственностью", "",
		"акционерное общество", "",
		"публичное акционерное общество", "",
		"ооо", "",
		"оао", "",
		"зао", "",
		"пао", "",
		"ао", "",
		"ип", "",
		`"`, "",
		"«", "",
		"»", "",
	}
	for i := 0; i < len(repl); i += 2 {
		s = strings.ReplaceAll(s, repl[i], repl[i+1])
	}

	s = reOrgClean.ReplaceAllString(s, "")
	return s
}

func orgNamesMatch(a, b string) bool {
	na := normalizeOrgName(a)
	nb := normalizeOrgName(b)

	// если вдруг rusprofile не дал имя — не блокируем
	if nb == "" {
		return true
	}
	// если пользователь ввёл пусто/мусор — точно не совпало
	if na == "" {
		return false
	}

	// строгое совпадение
	return na == nb
}

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

func qtyKeyboard() tgbotapi.ReplyKeyboardMarkup {
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

func itemsDoneKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnAddItem),
			tgbotapi.NewKeyboardButton(btnFinishItems),
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

	case stageAwaitItemName:
		n := len(st.Draft.Items) + 1
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Введите наименование позиции №%d:", n))
		msg.ReplyMarkup = stepControlKeyboard()
		_, _ = bot.Send(msg)

	case stageAwaitItemQty:
		msg := tgbotapi.NewMessage(chatID, "Введите количество (число). Можно «Пропуск» = 1:")
		msg.ReplyMarkup = qtyKeyboard()
		_, _ = bot.Send(msg)

	case stageAwaitItemUnit:
		msg := tgbotapi.NewMessage(chatID, "Введите единицу измерения (например: шт, кг, м, усл):")
		msg.ReplyMarkup = stepControlKeyboard()
		_, _ = bot.Send(msg)

	case stageAwaitItemUnitPrice:
		msg := tgbotapi.NewMessage(chatID, "Введите цену за единицу (например: 1000 или 1 000):")
		msg.ReplyMarkup = stepControlKeyboard()
		_, _ = bot.Send(msg)

	case stageAwaitItemLineTotal:
		var q string
		if st.CurItem.Qty == 1 {
			q = "Введите итоговую сумму по позиции (она же цена за единицу, т.к. количество = 1):"
		} else {
			q = "Введите ОБЩУЮ стоимость по позиции (итого по строке). Это НЕ цена за единицу:"
		}
		msg := tgbotapi.NewMessage(chatID, q)
		msg.ReplyMarkup = stepControlKeyboard()
		_, _ = bot.Send(msg)

	case stageAskMoreItems:
		msg := tgbotapi.NewMessage(chatID, "Добавить ещё позицию или завершить список?")
		msg.ReplyMarkup = itemsDoneKeyboard()
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

		// если есть имя из Rusprofile — проверяем совпадение
		if st.Draft.RusName != "" && !orgNamesMatch(txt, st.Draft.RusName) {
			msg := tgbotapi.NewMessage(
				m.Chat.ID,
				fmt.Sprintf(
					"По ИНН %s в Rusprofile организация указана как:\n%s\n\nПожалуйста, введите название юридического лица ещё раз (как в Rusprofile).",
					st.Draft.INN,
					st.Draft.RusName,
				),
			)
			msg.ReplyMarkup = stepControlKeyboard()
			_, _ = bot.Send(msg)
			return
		}

		// ✅ храним ввод пользователя для текста/сообщений
		st.Draft.LegalName = txt

		st.Stage = stageAwaitItemName
		st.CurItem = appItem{}
		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAwaitItemName:
		if txt == "" {
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		st.CurItem = appItem{Name: txt, Qty: 1}
		st.Stage = stageAwaitItemQty
		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAwaitItemQty:
		if txt == btnSkip {
			st.CurItem.Qty = 1
			st.Stage = stageAwaitItemUnit
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		if txt == "" {
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		q, qerr := strconv.ParseInt(strings.TrimSpace(txt), 10, 64)
		if qerr != nil || q <= 0 {
			msg := tgbotapi.NewMessage(m.Chat.ID, "Введите количество числом (например: 1, 2, 10) или нажмите «Пропуск».")
			msg.ReplyMarkup = qtyKeyboard()
			_, _ = bot.Send(msg)
			return
		}
		st.CurItem.Qty = q
		st.Stage = stageAwaitItemUnit
		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAwaitItemUnit:
		u := strings.TrimSpace(txt)
		if u == "" || u == btnSkip {
			// если пользователь нажал пропуск — оставим пусто, в счёте подставим "шт"
			u = ""
		}
		st.CurItem.Unit = u

		// если количество 1 — пропускаем ввод цены за единицу, спрашиваем только итог
		if st.CurItem.Qty == 1 {
			st.Stage = stageAwaitItemLineTotal
		} else {
			st.Stage = stageAwaitItemUnitPrice
		}

		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAwaitItemUnitPrice:
		if txt == "" {
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		p, perr := parseMoney(txt)
		if perr != nil {
			msg := tgbotapi.NewMessage(m.Chat.ID, "Не смог распознать цену. Пример: 1000 или 1 000")
			msg.ReplyMarkup = stepControlKeyboard()
			_, _ = bot.Send(msg)
			return
		}
		st.CurItem.UnitPrice = p
		st.Stage = stageAwaitItemLineTotal
		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAwaitItemLineTotal:
		if txt == "" {
			promptForStage(bot, m.Chat.ID, st)
			return
		}
		s, serr := parseMoney(txt)
		if serr != nil {
			msg := tgbotapi.NewMessage(m.Chat.ID, "Не смог распознать сумму. Пример: 1000000 или 1 000 000")
			msg.ReplyMarkup = stepControlKeyboard()
			_, _ = bot.Send(msg)
			return
		}
		// qty==1: введённая сумма = и цена за единицу, и итог
		if st.CurItem.Qty == 1 {
			st.CurItem.UnitPrice = s
			st.CurItem.Total = s
			st.Draft.Items = append(st.Draft.Items, st.CurItem)
			st.CurItem = appItem{}
			st.Stage = stageAskMoreItems
			promptForStage(bot, m.Chat.ID, st)
			return
		}

		// qty>1: проверка корректности суммы
		expected := float64(st.CurItem.Qty) * st.CurItem.UnitPrice

		if math.Abs(expected-s) > 0.0001 {
			// 1️⃣ первое сообщение — ТОЛЬКО про ошибку
			_, _ = bot.Send(tgbotapi.NewMessage(
				m.Chat.ID,
				fmt.Sprintf(
					"Сумма не сходится: %d × %.2f = %.2f, а вы ввели %.2f.",
					st.CurItem.Qty, st.CurItem.UnitPrice, expected, s,
				),
			))

			// 2️⃣ второе сообщение — инструкция + клавиатура
			msg2 := tgbotapi.NewMessage(
				m.Chat.ID,
				"Введите заново цену за единицу и итог по этой позиции.",
			)
			msg2.ReplyMarkup = stepControlKeyboard()
			_, _ = bot.Send(msg2)

			// возвращаемся на ввод цены
			st.CurItem.Total = 0
			st.Stage = stageAwaitItemUnitPrice
			return
		}

		st.CurItem.Total = s
		st.Draft.Items = append(st.Draft.Items, st.CurItem)
		st.CurItem = appItem{}
		st.Stage = stageAskMoreItems
		promptForStage(bot, m.Chat.ID, st)
		return

	case stageAskMoreItems:
		switch txt {
		case btnAddItem:
			st.Stage = stageAwaitItemName
			promptForStage(bot, m.Chat.ID, st)
			return
		case btnFinishItems:
			if len(st.Draft.Items) == 0 {
				st.Stage = stageAwaitItemName
				promptForStage(bot, m.Chat.ID, st)
				return
			}
			st.Stage = stageAwaitContract
			promptForStage(bot, m.Chat.ID, st)
			return
		default:
			msg := tgbotapi.NewMessage(m.Chat.ID, "Выберите вариант кнопкой снизу.")
			msg.ReplyMarkup = itemsDoneKeyboard()
			_, _ = bot.Send(msg)
			return
		}

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

	// считаем итоговую сумму
	total := 0.0
	for _, it := range st.Draft.Items {
		total += it.Total
	}
	st.Draft.TotalSum = total

	pos := []string{"Позиции:"}
	for i, it := range st.Draft.Items {
		pos = append(pos, fmt.Sprintf("%d) %s; кол-во=%d; ед=%s; цена=%.2f; итого=%.2f", i+1, it.Name, it.Qty, it.Unit, it.UnitPrice, it.Total))
	}

	parts := []string{
		"📝 Заявка на подтверждение",
		fmt.Sprintf("От: %s", user),
		fmt.Sprintf("Компания: %s", st.Draft.Company),
		fmt.Sprintf("ИНН: %s", st.Draft.INN),
		fmt.Sprintf("Юр.лицо (ввод): %s", st.Draft.LegalName),
		strings.Join(pos, "\n"),
		fmt.Sprintf("Сумма итого: %.2f", st.Draft.TotalSum),
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

	SendApplicationToApproval(bot, db, cfg, m.Chat.ID, m.MessageID, text, st.Draft)

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
