package cli

import (
	"encoding/json"
	"os"
)

func outputJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func outputError(err error) {
	json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()})
	os.Exit(1)
}
