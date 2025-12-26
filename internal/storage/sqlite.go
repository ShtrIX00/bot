package storage

import (
	"database/sql"
	"log"

	_ "modernc.org/sqlite"
)

func MustOpen(path string) *sql.DB {
	db, err := sql.Open("sqlite", path)
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

func migrate(db *sql.DB) error {
	// users
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

	// msg_map
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
