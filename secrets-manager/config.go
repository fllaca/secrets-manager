package secretsmanager

import (
	"time"
)

// Config holds the general global Secret manager config
type Config struct {
	ConfigMapRefreshInterval time.Duration
	BackendScrapeInterval    time.Duration
	ConfigMap                string
}
