package tg3

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// Если у тебя уже есть такой тип в другом файле - оставь только ОДНУ версию.
// Здесь он нужен для заполнения позиций.
type appItem struct {
	Name      string
	Qty       int64
	Unit      string
	UnitPrice float64
	Total     float64
}

func ddmmyyyy(d time.Time) string { return d.Format("02.01.2006") }

func ruDateWords(d time.Time) string {
	months := []string{
		"января", "февраля", "марта", "апреля", "мая", "июня",
		"июля", "августа", "сентября", "октября", "ноября", "декабря",
	}
	m := int(d.Month())
	mon := ""
	if m >= 1 && m <= 12 {
		mon = months[m-1]
	}
	return fmt.Sprintf("%d %s %d г.", d.Day(), mon, d.Year())
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func resolveTemplatePath(templatePath string) (string, error) {
	if fileExists(templatePath) {
		return templatePath, nil
	}

	exe, err := os.Executable()
	exeDir := ""
	if err == nil {
		exeDir = filepath.Dir(exe)
	}

	var tried []string
	tried = append(tried, templatePath)

	if exeDir != "" {
		c1 := filepath.Join(exeDir, templatePath)
		tried = append(tried, c1)
		if fileExists(c1) {
			return c1, nil
		}

		c2 := filepath.Join(exeDir, "..", templatePath)
		tried = append(tried, c2)
		if fileExists(c2) {
			return c2, nil
		}

		c3 := filepath.Join(exeDir, "..", "..", templatePath)
		tried = append(tried, c3)
		if fileExists(c3) {
			return c3, nil
		}
	}

	return "", fmt.Errorf("template not found. tried: %s", strings.Join(tried, " | "))
}

// ---------- Сумма прописью (без макросов) ----------

func plural(n int64, one, few, many string) string {
	n = n % 100
	if n >= 11 && n <= 19 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	default:
		return many
	}
}

func triadToWords(n int, feminine bool) []string {
	onesM := []string{"", "один", "два", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять"}
	onesF := []string{"", "одна", "две", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять"}
	tens10 := []string{"десять", "одиннадцать", "двенадцать", "тринадцать", "четырнадцать", "пятнадцать", "шестнадцать", "семнадцать", "восемнадцать", "девятнадцать"}
	tens := []string{"", "", "двадцать", "тридцать", "сорок", "пятьдесят", "шестьдесят", "семьдесят", "восемьдесят", "девяносто"}
	hunds := []string{"", "сто", "двести", "триста", "четыреста", "пятьсот", "шестьсот", "семьсот", "восемьсот", "девятьсот"}

	var out []string
	h := n / 100
	t := (n / 10) % 10
	u := n % 10

	if h > 0 {
		out = append(out, hunds[h])
	}
	if t == 1 {
		out = append(out, tens10[u])
		return out
	}
	if t > 1 {
		out = append(out, tens[t])
	}
	if u > 0 {
		if feminine {
			out = append(out, onesF[u])
		} else {
			out = append(out, onesM[u])
		}
	}
	return out
}

// moneyToWordsRU: "шестьсот шестьдесят шесть тысяч рублей 00 копеек"
func moneyToWordsRU(amount float64) string {
	amount = math.Round(amount*100) / 100
	rub := int64(amount)
	kop := int64(math.Round((amount - float64(rub)) * 100))
	if kop == 100 {
		rub++
		kop = 0
	}

	type scale struct {
		div      int64
		one      string
		few      string
		many     string
		feminine bool
	}
	scales := []scale{
		{1_000_000_000, "миллиард", "миллиарда", "миллиардов", false},
		{1_000_000, "миллион", "миллиона", "миллионов", false},
		{1_000, "тысяча", "тысячи", "тысяч", true},
	}

	var words []string
	rest := rub

	for _, sc := range scales {
		if rest >= sc.div {
			part := int(rest / sc.div)
			rest = rest % sc.div
			words = append(words, triadToWords(part, sc.feminine)...)
			words = append(words, plural(int64(part), sc.one, sc.few, sc.many))
		}
	}

	if rub == 0 {
		words = append(words, "ноль")
	} else if rest > 0 {
		words = append(words, triadToWords(int(rest), false)...)
	}

	words = append(words, plural(rub, "рубль", "рубля", "рублей"))
	words = append(words, fmt.Sprintf("%02d", kop), plural(kop, "копейка", "копейки", "копеек"))

	s := strings.Join(words, " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// ----- Твой основной метод заполнения -----

// applicationDraft должен быть ТВОЙ (из твоего проекта). Здесь используются поля:
// INN, LegalName, RusKPP, RusName, RusAddress, Contract.
func FillInvoiceTemplateXLSX(
	templatePath string,
	outDir string,
	invoiceNo int64,
	invoiceDate time.Time,
	draft applicationDraft,
	items []appItem,
) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("no items")
	}

	resolved, err := resolveTemplatePath(templatePath)
	if err != nil {
		return "", fmt.Errorf("open template: %w", err)
	}

	f, err := excelize.OpenFile(resolved)
	if err != nil {
		return "", fmt.Errorf("open template: %w", err)
	}
	defer f.Close()

	// ожидаем лист "schet"
	sheet := "schet"
	if _, e := f.GetSheetIndex(sheet); e != nil {
		sheet = f.GetSheetName(0)
	}

	// A9: "Счёт на оплату № N от 20 января 2026 г."
	_ = f.SetCellValue(sheet, "A9", fmt.Sprintf("Счёт на оплату № %d от %s", invoiceNo, ruDateWords(invoiceDate)))

	// E13: "название юр. лица, ИНН, КПП, Адрес"
	name := strings.TrimSpace(draft.RusName)
	if name == "" {
		name = strings.TrimSpace(draft.LegalName)
	}
	inn := strings.TrimSpace(draft.INN)
	kpp := strings.TrimSpace(draft.RusKPP)
	addr := strings.TrimSpace(draft.RusAddress)

	var e13parts []string
	if name != "" {
		e13parts = append(e13parts, name)
	}
	if inn != "" {
		e13parts = append(e13parts, "ИНН "+inn)
	}
	if kpp != "" {
		e13parts = append(e13parts, "КПП "+kpp)
	}
	if addr != "" {
		e13parts = append(e13parts, addr)
	}
	_ = f.SetCellValue(sheet, "E13", strings.Join(e13parts, ", "))

	// E15: "номер договора от 20.01.2026"
	contract := strings.TrimSpace(draft.Contract)
	if contract != "" && contract != "0" {
		_ = f.SetCellValue(sheet, "E15", fmt.Sprintf("%s от %s", contract, ddmmyyyy(invoiceDate)))
	} else {
		_ = f.SetCellValue(sheet, "E15", "")
	}

	// Позиции: первая строка 18
	startRow := 18
	if len(items) > 1 {
		need := len(items) - 1
		// вставляем строки после 18-й, чтобы сдвинуть итоги ниже
		if err := f.InsertRows(sheet, 19, need); err != nil {
			return "", fmt.Errorf("insert rows: %w", err)
		}
	}

	var total float64
	for i, it := range items {
		row := startRow + i

		if strings.TrimSpace(it.Name) == "" {
			it.Name = "-"
		}
		if it.Qty <= 0 {
			it.Qty = 1
		}
		if strings.TrimSpace(it.Unit) == "" {
			it.Unit = "шт"
		}

		it.UnitPrice = math.Round(it.UnitPrice*100) / 100
		it.Total = math.Round(it.Total*100) / 100

		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), it.Name)      // наименование
		_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", row), it.Qty)       // количество
		_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", row), it.Unit)      // ед.изм
		_ = f.SetCellValue(sheet, fmt.Sprintf("O%d", row), it.UnitPrice) // цена
		_ = f.SetCellValue(sheet, fmt.Sprintf("P%d", row), it.Total)     // сумма

		total += it.Total
	}

	total = math.Round(total*100) / 100

	// ✅ НДС 22% по твоей формуле: total/122*22
	vat := math.Round((total/122.0*22.0)*100) / 100

	// Итоги по твоему шаблону: P20/P21/P22 + A23/A24/A26
	offset := len(items) - 1
	p20 := 20 + offset
	p21 := 21 + offset
	p22 := 22 + offset
	a23 := 23 + offset
	a24 := 24 + offset
	a26 := 26 + offset

	_ = f.SetCellValue(sheet, fmt.Sprintf("P%d", p20), total) // Итого
	_ = f.SetCellValue(sheet, fmt.Sprintf("P%d", p21), vat)   // ✅ НДС (сам НДС)
	_ = f.SetCellValue(sheet, fmt.Sprintf("P%d", p22), total) // Всего к оплате

	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", a23), fmt.Sprintf("Всего наименований %d, на сумму %.2f", len(items), total))

	// ✅ A24: всегда записываем текст суммы (не формула!)
	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", a24), capitalizeFirst(moneyToWordsRU(total)))

	// A26: меняем только дату внутри строки
	oldA26, _ := f.GetCellValue(sheet, fmt.Sprintf("A%d", a26))
	if strings.TrimSpace(oldA26) == "" {
		oldA26 = "Оплатить не позднее " + ddmmyyyy(invoiceDate)
	}
	re := regexp.MustCompile(`\b\d{2}\.\d{2}\.\d{4}\b`)
	newA26 := re.ReplaceAllString(oldA26, ddmmyyyy(invoiceDate))
	if newA26 == oldA26 {
		newA26 = "Оплатить не позднее " + ddmmyyyy(invoiceDate) + " " + strings.TrimSpace(oldA26)
	}
	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", a26), newA26)

	if outDir == "" {
		outDir = os.TempDir()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}

	outPath := filepath.Join(outDir, fmt.Sprintf("invoice_%d.xlsx", invoiceNo))
	if err := f.SaveAs(outPath); err != nil {
		return "", fmt.Errorf("save xlsx: %w", err)
	}
	return outPath, nil
}

func capitalizeFirst(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}
