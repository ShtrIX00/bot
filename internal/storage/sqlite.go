package storage

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"

	_ "modernc.org/sqlite"
)

func MustOpen(path string) *sql.DB {
	// ✅ параметры для многопроцессного доступа (bot1/bot2/bot3)
	dsn := withSQLiteParams(path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("cannot open sqlite db: %v", err)
	}

	if err := db.Ping(); err != nil {
		log.Fatalf("cannot ping sqlite db: %v", err)
	}

	if err := migrate(db); err != nil {
		log.Fatalf("cannot run migrations: %v", err)
	}

	return db
}

func withSQLiteParams(path string) string {
	// modernc.org/sqlite понимает query-параметры
	// journal_mode=WAL снижает lock’и, busy_timeout помогает переживать конкуренцию.
	q := url.Values{}
	q.Set("_journal_mode", "WAL")
	q.Set("_busy_timeout", "5000")
	q.Set("_foreign_keys", "1")

	return fmt.Sprintf("%s?%s", path, q.Encode())
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS users (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  telegram_id INTEGER NOT NULL UNIQUE,
  chat_id     INTEGER NOT NULL,
  username    TEXT,
  first_name  TEXT,
  last_name   TEXT,
  company     INTEGER NOT NULL DEFAULT 0,
  allowed     INTEGER NOT NULL DEFAULT 0,
  blocked     INTEGER NOT NULL DEFAULT 0,
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS msg_map (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  src_chat_id     INTEGER NOT NULL,
  src_message_id  INTEGER NOT NULL,
  user_chat_id    INTEGER NOT NULL,
  user_message_id INTEGER NOT NULL,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(src_chat_id, src_message_id)
);
`)
	if err != nil {
		return err
	}

	return nil
}
