package validate

// SecurityConfig represents the security configuration for validation purposes.
type SecurityConfig struct {
	EnableTLS       bool
	PrivateKeyFile  string
	CertificateFile string
}

// ValidateSecurity performs validation of the security configuration.
func ValidateSecurity(sec SecurityConfig) []error {
	var errs []error

	// Validate logging level
	if sec.EnableTLS {
		if sec.PrivateKeyFile == "" {
			errs = append(errs, ValidationError{
				Path:    "security.private_key_file",
				Message: "required when enable_tls is true",
			})
		} else {
			if err := ValidateFileReadable(sec.PrivateKeyFile); err != nil {
				errs = append(errs, ValidationError{
					Path:    "security.private_key_file",
					Message: err.Error(),
				})
			}
		}

		if sec.CertificateFile == "" {
			errs = append(errs, ValidationError{
				Path:    "security.certificate_file",
				Message: "required when enable_tls is true",
			})
		} else {
			if err := ValidateFileReadable(sec.CertificateFile); err != nil {
				errs = append(errs, ValidationError{
					Path:    "security.certificate_file",
					Message: err.Error(),
				})
			}
		}
	}

	return errs
}
