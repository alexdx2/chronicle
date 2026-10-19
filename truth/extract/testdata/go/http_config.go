package config

import (
	"cmp"
	"net/http"
	"os"
)

const defaultBaseURL = "https://api.payments.example/v1"

type Config struct {
	Inventory string
	Docs      string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() Config {
	_ = getenv("PORT", ":8080")
	_ = getenv("DB_URL", "postgres://db:5432/app")
	_ = getEnvOr(
		"SHIPPING_URL",
		"http://shipping-svc:9000/api",
	)
	_ = getenv("LOCAL_URL", "http://localhost:8080")
	_ = cmp.Or(os.Getenv("TAX_URL"), "https://tax.internal.example")
	_ = getenv("API_DOCS_URL", "https://docs.example.com/guide")
	// _ = getenv("OLD_URL", "http://old-svc")
	_ = prevent("x", "http://not-env.example")
	return Config{Inventory: getenv("INVENTORY_URL", "http://inventory-svc:8080")}
}

func ledger() string {
	l := os.Getenv("LEDGER_URL")
	if l == "" {
		l = "http://ledger-svc:7000"
	}
	return l
}

func notifier() string {
	if v := os.Getenv("NOTIFY_URL"); v != "" {
		return v
	}
	return "https://notify.example.org"
}

func call(c *http.Client) {
	http.Get("https://weather.example.net/today")
	c.Post("http://billing-svc/charge", "application/json", nil)
	req, _ := http.NewRequest("GET", "https://search.example.io/q", nil)
	_ = req
	http.NewRequestWithContext(nil, http.MethodGet, "http://reports-svc:8081/r", nil)
	http.Get("http://127.0.0.1:9/skip")
	http.Get("/relative")
	// http.Get("http://commented.example.com")
	println("http.Get(\"http://in-string.example.com\")")
}
