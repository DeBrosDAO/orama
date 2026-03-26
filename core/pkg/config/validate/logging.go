package validate

import (
	"fmt"
	"path/filepath"
)

// LoggingConfig represents the logging configuration for validation purposes.
type LoggingConfig struct {
	Level      string
	Format     string
	OutputFile string
}

// ValidateLogging performs validation of the logging configuration.
func ValidateLogging(log LoggingConfig) []error {
	var errs []error

	// Validate level
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[log.Level] {
		errs = append(errs, ValidationError{
			Path:    "logging.level",
			Message: fmt.Sprintf("invalid value %q", log.Level),
			Hint:    "allowed values: debug, info, warn, error",
		})
	}

	// Validate format
	validFormats := map[string]bool{"json": true, "console": true}
	if !validFormats[log.Format] {
		errs = append(errs, ValidationError{
			Path:    "logging.format",
			Message: fmt.Sprintf("invalid value %q", log.Format),
			Hint:    "allowed values: json, console",
		})
	}

	// Validate output_file
	if log.OutputFile != "" {
		dir := filepath.Dir(log.OutputFile)
		if dir != "" && dir != "." {
			if err := ValidateDirWritable(dir); err != nil {
				errs = append(errs, ValidationError{
					Path:    "logging.output_file",
					Message: fmt.Sprintf("parent directory not writable: %v", err),
				})
			}
		}
	}

	return errs
}
