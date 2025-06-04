package database

import (
	"fmt"
	"log"
)

// GetAllConfig retrieves all configuration entries from the database
func (db *SQLiteDB) GetAllConfig() (map[string]string, error) {
	rows, err := db.db.Query("SELECT key, value FROM config")
	if err != nil {
		return nil, fmt.Errorf("failed to query config: %v", err)
	}
	defer rows.Close()

	config := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("failed to scan config row: %v", err)
		}
		config[key] = value
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating config rows: %v", err)
	}

	return config, nil
}

// GetConfig retrieves a single configuration value by key
func (db *SQLiteDB) GetConfig(key string) (string, error) {
	log.Printf("GetConfig: Retrieving config for key: %s", key)
	var value string
	err := db.db.QueryRow("SELECT value FROM config WHERE key = ?", key).Scan(&value)
	if err != nil {
		log.Printf("GetConfig: Error retrieving config for key %s: %v", key, err)
		return "", fmt.Errorf("failed to get config for key %s: %v", key, err)
	}
	log.Printf("GetConfig: Successfully retrieved config for key %s", key)
	return value, nil
}

// SetConfig sets a configuration value, creating it if it doesn't exist
func (db *SQLiteDB) SetConfig(key, value string) error {
	log.Printf("SetConfig: Setting config for key: %s", key)
	
	// Use INSERT OR REPLACE to handle both insert and update cases
	_, err := db.db.Exec(
		"INSERT OR REPLACE INTO config (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)",
		key, value,
	)
	if err != nil {
		log.Printf("SetConfig: Error setting config for key %s: %v", key, err)
		return fmt.Errorf("failed to set config %s: %v", key, err)
	}
	log.Printf("SetConfig: Successfully updated config for key: %s", key)
	return nil
}

// DeleteConfig removes a configuration entry by key
func (db *SQLiteDB) DeleteConfig(key string) error {
	_, err := db.db.Exec("DELETE FROM config WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("failed to delete config %s: %v", key, err)
	}
	return nil
}
