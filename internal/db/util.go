package db

import (
	"encoding/json"
	"time"

	"keyhive/internal/model"
)

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func marshalFields(f []model.Field) (string, error) {
	b, err := json.Marshal(f)
	return string(b), err
}
