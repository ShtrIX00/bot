package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Bot1Token string
	Bot2Token string
	Bot3Token string

	AccessPassword string

	// bot1
	Accounting1ChatID   int64
	Accounting2ChatID   int64
	Accounting3ChatID   int64
	Accounting4ChatID   int64
	Bot1NavigatorChatID int64

	// bot2
	Bot2AccountingChatID int64
	Bot2NavigatorChatID  int64

	// bot3
	Bot3NavigatorChatID int64
	Bot3ApprovalChatID  int64

	ResponderIDs     map[int64]bool
	ResponderAliases map[int64]string
}

func MustLoad() *Config {
	cfg := &Config{
		Bot1Token: os.Getenv("BOT1_TOKEN"),
		Bot2Token: os.Getenv("BOT2_TOKEN"),
		Bot3Token: os.Getenv("BOT3_TOKEN"),

		AccessPassword: os.Getenv("ACCESS_PASSWORD"),

		// ✅ как у тебя в .env: ACCOUNTING_1_CHAT_ID ...
		Accounting1ChatID: mustInt64("ACCOUNTING_1_CHAT_ID"),
		Accounting2ChatID: mustInt64("ACCOUNTING_2_CHAT_ID"),
		Accounting3ChatID: mustInt64("ACCOUNTING_3_CHAT_ID"),
		Accounting4ChatID: mustInt64("ACCOUNTING_4_CHAT_ID"),

		Bot1NavigatorChatID: mustInt64("BOT1_NAVIGATOR_CHAT_ID"),

		Bot2AccountingChatID: mustInt64("BOT2_ACCOUNTING_CHAT_ID"),
		Bot2NavigatorChatID:  mustInt64("BOT2_NAVIGATOR_CHAT_ID"),

		Bot3NavigatorChatID: mustInt64("BOT3_NAVIGATOR_CHAT_ID"),
		Bot3ApprovalChatID:  mustInt64("BOT3_APPROVAL_CHAT_ID"),
	}

	cfg.ResponderIDs = parseIDs(os.Getenv("RESPONDER_IDS"))
	cfg.ResponderAliases = parseAliases(os.Getenv("RESPONDER_ALIASES"))
	return cfg
}

func mustInt64(key string) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.Fatalf("bad %s: %v", key, err)
	}
	return n
}

func parseIDs(s string) map[int64]bool {
	out := map[int64]bool{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	parts := strings.Split(s, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			continue
		}
		out[id] = true
	}
	return out
}

func parseAliases(s string) map[int64]string {
	out := map[int64]string{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSpace(kv[0]), 10, 64)
		if err != nil {
			continue
		}
		out[id] = strings.TrimSpace(kv[1])
	}
	return out
}
