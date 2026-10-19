package config

func testing() { getenv("X_URL", "http://only-in-test.example.com") }
