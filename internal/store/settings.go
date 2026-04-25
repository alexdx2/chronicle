package store

import "fmt"

func (s *Store) GetSetting(key string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM project_settings WHERE key = ?", key).Scan(&val)
	if err != nil {
		return "", fmt.Errorf("setting %q not found", key)
	}
	return val, nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO project_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?",
		key, value, value,
	)
	return err
}
