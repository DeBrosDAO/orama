package serverless

import (
	"time"
)

// Config holds configuration for the serverless engine.
type Config struct {
	// Memory limits
	DefaultMemoryLimitMB int `yaml:"default_memory_limit_mb"`
	MaxMemoryLimitMB     int `yaml:"max_memory_limit_mb"`

	// Execution limits
	DefaultTimeoutSeconds int `yaml:"default_timeout_seconds"`
	MaxTimeoutSeconds     int `yaml:"max_timeout_seconds"`

	// Retry configuration
	DefaultRetryCount        int `yaml:"default_retry_count"`
	MaxRetryCount            int `yaml:"max_retry_count"`
	DefaultRetryDelaySeconds int `yaml:"default_retry_delay_seconds"`

	// Rate limiting (global)
	GlobalRateLimitPerMinute int `yaml:"global_rate_limit_per_minute"`

	// Background job configuration
	JobWorkers        int           `yaml:"job_workers"`
	JobPollInterval   time.Duration `yaml:"job_poll_interval"`
	JobMaxQueueSize   int           `yaml:"job_max_queue_size"`
	JobMaxPayloadSize int           `yaml:"job_max_payload_size"` // bytes

	// Scheduler configuration
	CronPollInterval  time.Duration `yaml:"cron_poll_interval"`
	TimerPollInterval time.Duration `yaml:"timer_poll_interval"`
	DBPollInterval    time.Duration `yaml:"db_poll_interval"`

	// WASM compilation cache
	ModuleCacheSize int  `yaml:"module_cache_size"` // Number of compiled modules to cache
	EnablePrewarm   bool `yaml:"enable_prewarm"`    // Pre-compile frequently used functions

	// Secrets encryption
	SecretsEncryptionKey string `yaml:"secrets_encryption_key"` // AES-256 key (32 bytes, hex-encoded)

	// Logging
	LogInvocations bool `yaml:"log_invocations"` // Log all invocations to database
	LogRetention   int  `yaml:"log_retention"`   // Days to retain logs
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		// Memory limits
		DefaultMemoryLimitMB: 64,
		MaxMemoryLimitMB:     256,

		// Execution limits
		DefaultTimeoutSeconds: 30,
		MaxTimeoutSeconds:     300, // 5 minutes max

		// Retry configuration
		DefaultRetryCount:        0,
		MaxRetryCount:            5,
		DefaultRetryDelaySeconds: 5,

		// Rate limiting
		GlobalRateLimitPerMinute: 10000, // 10k requests/minute globally

		// Background jobs
		JobWorkers:        4,
		JobPollInterval:   time.Second,
		JobMaxQueueSize:   10000,
		JobMaxPayloadSize: 1024 * 1024, // 1MB

		// Scheduler
		CronPollInterval:  time.Minute,
		TimerPollInterval: time.Second,
		DBPollInterval:    time.Second * 5,

		// WASM cache
		ModuleCacheSize: 100,
		EnablePrewarm:   true,

		// Logging
		LogInvocations: true,
		LogRetention:   7, // 7 days
	}
}

// Validate checks the configuration for errors.
func (c *Config) Validate() []error {
	var errs []error

	if c.DefaultMemoryLimitMB <= 0 {
		errs = append(errs, &ConfigError{Field: "DefaultMemoryLimitMB", Message: "must be positive"})
	}
	if c.MaxMemoryLimitMB < c.DefaultMemoryLimitMB {
		errs = append(errs, &ConfigError{Field: "MaxMemoryLimitMB", Message: "must be >= DefaultMemoryLimitMB"})
	}
	if c.DefaultTimeoutSeconds <= 0 {
		errs = append(errs, &ConfigError{Field: "DefaultTimeoutSeconds", Message: "must be positive"})
	}
	if c.MaxTimeoutSeconds < c.DefaultTimeoutSeconds {
		errs = append(errs, &ConfigError{Field: "MaxTimeoutSeconds", Message: "must be >= DefaultTimeoutSeconds"})
	}
	if c.GlobalRateLimitPerMinute <= 0 {
		errs = append(errs, &ConfigError{Field: "GlobalRateLimitPerMinute", Message: "must be positive"})
	}
	if c.JobWorkers <= 0 {
		errs = append(errs, &ConfigError{Field: "JobWorkers", Message: "must be positive"})
	}
	if c.ModuleCacheSize <= 0 {
		errs = append(errs, &ConfigError{Field: "ModuleCacheSize", Message: "must be positive"})
	}

	return errs
}

// ApplyDefaults fills in zero values with defaults.
func (c *Config) ApplyDefaults() {
	defaults := DefaultConfig()

	if c.DefaultMemoryLimitMB == 0 {
		c.DefaultMemoryLimitMB = defaults.DefaultMemoryLimitMB
	}
	if c.MaxMemoryLimitMB == 0 {
		c.MaxMemoryLimitMB = defaults.MaxMemoryLimitMB
	}
	if c.DefaultTimeoutSeconds == 0 {
		c.DefaultTimeoutSeconds = defaults.DefaultTimeoutSeconds
	}
	if c.MaxTimeoutSeconds == 0 {
		c.MaxTimeoutSeconds = defaults.MaxTimeoutSeconds
	}
	if c.GlobalRateLimitPerMinute == 0 {
		c.GlobalRateLimitPerMinute = defaults.GlobalRateLimitPerMinute
	}
	if c.JobWorkers == 0 {
		c.JobWorkers = defaults.JobWorkers
	}
	if c.JobPollInterval == 0 {
		c.JobPollInterval = defaults.JobPollInterval
	}
	if c.JobMaxQueueSize == 0 {
		c.JobMaxQueueSize = defaults.JobMaxQueueSize
	}
	if c.JobMaxPayloadSize == 0 {
		c.JobMaxPayloadSize = defaults.JobMaxPayloadSize
	}
	if c.CronPollInterval == 0 {
		c.CronPollInterval = defaults.CronPollInterval
	}
	if c.TimerPollInterval == 0 {
		c.TimerPollInterval = defaults.TimerPollInterval
	}
	if c.DBPollInterval == 0 {
		c.DBPollInterval = defaults.DBPollInterval
	}
	if c.ModuleCacheSize == 0 {
		c.ModuleCacheSize = defaults.ModuleCacheSize
	}
	if c.LogRetention == 0 {
		c.LogRetention = defaults.LogRetention
	}
}

// WithMemoryLimit returns a copy with the memory limit set.
func (c *Config) WithMemoryLimit(defaultMB, maxMB int) *Config {
	copy := *c
	copy.DefaultMemoryLimitMB = defaultMB
	copy.MaxMemoryLimitMB = maxMB
	return &copy
}

// WithTimeout returns a copy with the timeout set.
func (c *Config) WithTimeout(defaultSec, maxSec int) *Config {
	copy := *c
	copy.DefaultTimeoutSeconds = defaultSec
	copy.MaxTimeoutSeconds = maxSec
	return &copy
}

// WithRateLimit returns a copy with the rate limit set.
func (c *Config) WithRateLimit(perMinute int) *Config {
	copy := *c
	copy.GlobalRateLimitPerMinute = perMinute
	return &copy
}

