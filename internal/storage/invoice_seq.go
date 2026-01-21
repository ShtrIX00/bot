package storage

import (
	"database/sql"
	"fmt"
)

// NextInvoiceNumber atomically reserves the next invoice number.
// Requirement: must not generate duplicate numbers even with multiple running bots.
func NextInvoiceNumber(db *sql.DB) (int64, error) {
	// SQLite supports atomic UPDATE .. RETURNING.
	// This avoids race conditions across processes.
	var reserved int64
	err := db.QueryRow(`
		UPDATE invoice_seq
		SET next_num = next_num + 1
		WHERE id = 1
		RETURNING next_num - 1;
	`).Scan(&reserved)
	if err == nil {
		return reserved, nil
	}

	// Fallback (in case RETURNING is not supported in a given SQLite build):
	// do best-effort with a single transaction.
	tx, txErr := db.Begin()
	if txErr != nil {
		return 0, fmt.Errorf("begin tx: %w", txErr)
	}
	defer tx.Rollback()

	var n int64
	if e := tx.QueryRow(`SELECT next_num FROM invoice_seq WHERE id=1;`).Scan(&n); e != nil {
		return 0, e
	}
	if _, e := tx.Exec(`UPDATE invoice_seq SET next_num=? WHERE id=1;`, n+1); e != nil {
		return 0, e
	}
	if e := tx.Commit(); e != nil {
		return 0, e
	}
	return n, nil
}
