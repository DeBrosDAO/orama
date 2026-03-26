package config

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level      string `yaml:"level"`       // debug, info, warn, error
	Format     string `yaml:"format"`      // json, console
	OutputFile string `yaml:"output_file"` // Empty for stdout
}
