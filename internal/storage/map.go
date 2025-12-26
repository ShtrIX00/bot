package storage

import (
	"database/sql"
	"fmt"
	"strings"
)

type ReplyTarget struct {
	UserChatID    int64
	UserMessageID int
}

// запоминаем: на какое сообщение в группе/у навигатора ответили,
// и какому пользователю и какому message_id это соответствует
func AddMap(db *sql.DB, srcChatID int64, srcMessageID int, userChatID int64, userMessageID int) error {
	_, err := db.Exec(`
INSERT OR IGNORE INTO msg_map (src_chat_id, src_message_id, user_chat_id, user_message_id)
VALUES (?, ?, ?, ?);
`, srcChatID, srcMessageID, userChatID, userMessageID)
	if err != nil {
		return fmt.Errorf("add map: %w", err)
	}
	return nil
}

func GetReplyTarget(db *sql.DB, srcChatID int64, srcMessageID int) (*ReplyTarget, bool, error) {
	row := db.QueryRow(`
SELECT user_chat_id, user_message_id
FROM msg_map
WHERE src_chat_id=? AND src_message_id=?
LIMIT 1;
`, srcChatID, srcMessageID)

	var t ReplyTarget
	if err := row.Scan(&t.UserChatID, &t.UserMessageID); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &t, true, nil
}

// Найти, в какую из групп (по списку chatIDs) было отправлено пользовательское сообщение.
// Возвращает chat_id этой группы.
func FindMappedChatForUserMessage(db *sql.DB, userChatID int64, userMessageID int, chatIDs []int64) (int64, bool, error) {
	if len(chatIDs) == 0 {
		return 0, false, nil
	}

	ph := strings.TrimRight(strings.Repeat("?,", len(chatIDs)), ",")
	args := make([]any, 0, 2+len(chatIDs))
	args = append(args, userChatID, userMessageID)
	for _, id := range chatIDs {
		args = append(args, id)
	}

	q := fmt.Sprintf(`
SELECT src_chat_id
FROM msg_map
WHERE user_chat_id=? AND user_message_id=? AND src_chat_id IN (%s)
LIMIT 1;
`, ph)

	row := db.QueryRow(q, args...)
	var chatID int64
	if err := row.Scan(&chatID); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	return chatID, true, nil
}

// Найти message_id в конкретной группе, соответствующий пользовательскому сообщению.
// Мы берём MAX(src_message_id), чтобы попасть на "форвард" (он обычно идёт после заголовка).
func GetMappedGroupMessageID(db *sql.DB, groupChatID int64, userChatID int64, userMessageID int) (int, bool, error) {
	row := db.QueryRow(`
SELECT MAX(src_message_id)
FROM msg_map
WHERE src_chat_id=? AND user_chat_id=? AND user_message_id=?;
`, groupChatID, userChatID, userMessageID)

	var mid sql.NullInt64
	if err := row.Scan(&mid); err != nil {
		return 0, false, err
	}
	if !mid.Valid {
		return 0, false, nil
	}
	return int(mid.Int64), true, nil
}
