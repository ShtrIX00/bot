package storage

import (
	"database/sql"
	"fmt"
	"strings"
)

type User struct {
	ID         int64
	TelegramID int64
	ChatID     int64
	Username   *string
	FirstName  *string
	LastName   *string
	Company    int
	Allowed    bool
	Blocked    bool
}

func UpsertUser(db *sql.DB, u *User) error {
	_, err := db.Exec(`
INSERT INTO users (telegram_id, chat_id, username, first_name, last_name)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(telegram_id) DO UPDATE SET
  chat_id=excluded.chat_id,
  username=excluded.username,
  first_name=excluded.first_name,
  last_name=excluded.last_name;
`, u.TelegramID, u.ChatID, u.Username, u.FirstName, u.LastName)
	if err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}
	return nil
}

func ListAllUserChatIDs(db *sql.DB) ([]int64, error) {
	rows, err := db.Query(`SELECT chat_id FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var chatID int64
		if err := rows.Scan(&chatID); err != nil {
			return nil, err
		}
		out = append(out, chatID)
	}
	return out, nil
}

func GetUserCompanyByTelegramID(db *sql.DB, telegramID int64) (int, error) {
	row := db.QueryRow(`SELECT company FROM users WHERE telegram_id = ?`, telegramID)
	var company int
	err := row.Scan(&company)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return company, nil
}

func SetUserCompanyByTelegramID(db *sql.DB, telegramID int64, company int) error {
	_, err := db.Exec(`UPDATE users SET company = ? WHERE telegram_id = ?`, company, telegramID)
	return err
}

func IsUserAllowedByTelegramID(db *sql.DB, telegramID int64) (bool, error) {
	row := db.QueryRow(`SELECT allowed FROM users WHERE telegram_id = ?`, telegramID)
	var allowed int
	err := row.Scan(&allowed)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return allowed == 1, nil
}

func SetUserAllowedByTelegramID(db *sql.DB, telegramID int64, allowed bool) error {
	v := 0
	if allowed {
		v = 1
	}
	_, err := db.Exec(`UPDATE users SET allowed = ? WHERE telegram_id = ?`, v, telegramID)
	return err
}

func IsUserBlockedByTelegramID(db *sql.DB, telegramID int64) (bool, error) {
	row := db.QueryRow(`SELECT blocked FROM users WHERE telegram_id = ?`, telegramID)
	var blocked int
	err := row.Scan(&blocked)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return blocked == 1, nil
}

func SetUserBlockedByTelegramID(db *sql.DB, telegramID int64, blocked bool) error {
	v := 0
	if blocked {
		v = 1
	}
	_, err := db.Exec(`UPDATE users SET blocked = ? WHERE telegram_id = ?`, v, telegramID)
	return err
}

func GetUserChatIDByTelegramID(db *sql.DB, telegramID int64) (int64, bool, error) {
	row := db.QueryRow(`SELECT chat_id FROM users WHERE telegram_id = ?`, telegramID)
	var chatID int64
	err := row.Scan(&chatID)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return chatID, true, nil
}

func GetTelegramIDByUsername(db *sql.DB, username string) (int64, bool, error) {
	u := strings.TrimSpace(username)
	u = strings.TrimPrefix(u, "@")
	u = strings.ToLower(u)
	if u == "" {
		return 0, false, nil
	}

	row := db.QueryRow(`SELECT telegram_id FROM users WHERE lower(username)=? LIMIT 1`, u)
	var id int64
	err := row.Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func GetUsernameByTelegramID(db *sql.DB, telegramID int64) (string, bool, error) {
	row := db.QueryRow(`SELECT username FROM users WHERE telegram_id=? LIMIT 1`, telegramID)
	var s sql.NullString
	if err := row.Scan(&s); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	if !s.Valid || strings.TrimSpace(s.String) == "" {
		return "", false, nil
	}
	return strings.TrimSpace(s.String), true, nil
}

func GetEligibleUserChatIDByUsername(db *sql.DB, username string) (int64, bool, error) {
	u := strings.TrimSpace(username)
	u = strings.TrimPrefix(u, "@")
	u = strings.ToLower(u)
	if u == "" {
		return 0, false, nil
	}

	row := db.QueryRow(`
SELECT chat_id
FROM users
WHERE lower(username)=? AND allowed=1 AND blocked=0
LIMIT 1
`, u)

	var chatID int64
	if err := row.Scan(&chatID); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	return chatID, true, nil
}

func GetEligibleUserChatIDByTelegramID(db *sql.DB, telegramID int64) (int64, bool, error) {
	row := db.QueryRow(`
SELECT chat_id
FROM users
WHERE telegram_id=? AND allowed=1 AND blocked=0
LIMIT 1
`, telegramID)

	var chatID int64
	if err := row.Scan(&chatID); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	return chatID, true, nil
}

func ListAllowedUserChatIDs(db *sql.DB) ([]int64, error) {
	rows, err := db.Query(`
SELECT chat_id
FROM users
WHERE allowed = 1
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			return nil, err
		}
		out = append(out, cid)
	}
	return out, nil
}

func ListAllowedNotBlockedUserChatIDs(db *sql.DB) ([]int64, error) {
	rows, err := db.Query(`
SELECT chat_id
FROM users
WHERE allowed = 1 AND blocked = 0
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			return nil, err
		}
		out = append(out, cid)
	}
	return out, nil
}
