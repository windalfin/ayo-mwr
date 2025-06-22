package database

import "fmt"

// GetArduinoConfig returns port and baud from arduino_config table. If not present returns sql.ErrNoRows
func (s *SQLiteDB) GetArduinoConfig() (string, int, error) {
    row := s.db.QueryRow(`SELECT port, baud_rate FROM arduino_config LIMIT 1`)
    var port string
    var baud int
    err := row.Scan(&port, &baud)
    if err != nil {
        return "", 0, err
    }
    return port, baud, nil
}

// UpsertArduinoConfig inserts or updates the single-row arduino_config table
func (s *SQLiteDB) UpsertArduinoConfig(port string, baud int) error {
    // use replace into to handle both insert and update
    res, err := s.db.Exec(`REPLACE INTO arduino_config(id, port, baud_rate) VALUES (1, ?, ?)`, port, baud)
    if err != nil {
        return fmt.Errorf("upsert arduino_config: %w", err)
    }
    _ = res // ignore affected rows
    return nil
}
