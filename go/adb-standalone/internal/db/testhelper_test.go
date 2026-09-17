package db

import (
	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
)

func mustSurrealConfig(url, ns, database, user, pass string) *config.SurrealConfig {
	return &config.SurrealConfig{
		URL:      url,
		NS:       ns,
		DB:       database,
		Username: user,
		Password: pass,
	}
}
