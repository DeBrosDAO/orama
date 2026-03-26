package config

// SecurityConfig contains security-related configuration
type SecurityConfig struct {
	EnableTLS       bool   `yaml:"enable_tls"`
	PrivateKeyFile  string `yaml:"private_key_file"`
	CertificateFile string `yaml:"certificate_file"`
}
