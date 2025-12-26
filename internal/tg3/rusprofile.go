package tg3

import (
	"errors"
	"html"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type CompanyData struct {
	KPP     string
	Name    string
	Address string
}

func ParseRusprofileFromHTML(htmlText string) (*CompanyData, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlText))
	if err != nil {
		return nil, err
	}

	kpp := strings.TrimSpace(doc.Find("span#clip_kpp").First().Text())

	reName := regexp.MustCompile(`company\s*:\s*\{[\s\S]*?name\s*:\s*'([^']*)'`)
	reAddr := regexp.MustCompile(`company\s*:\s*\{[\s\S]*?address\s*:\s*'([^']*)'`)

	var name, address string

	if m := reName.FindStringSubmatch(htmlText); len(m) > 1 {
		name = html.UnescapeString(strings.TrimSpace(m[1]))
	}
	if m := reAddr.FindStringSubmatch(htmlText); len(m) > 1 {
		address = html.UnescapeString(strings.TrimSpace(m[1]))
	}

	if kpp == "" && name == "" && address == "" {
		return nil, errors.New("данные не найдены (возможно изменился HTML)")
	}

	return &CompanyData{
		KPP:     kpp,
		Name:    name,
		Address: address,
	}, nil
}
