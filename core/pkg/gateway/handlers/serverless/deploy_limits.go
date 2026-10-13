package serverless

import (
	"fmt"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// Form and metadata field names a deploy limit is reported under.
const (
	fieldMemoryLimitMB     = "memory_limit_mb"
	fieldTimeoutSeconds    = "timeout_seconds"
	fieldRetryCount        = "retry_count"
	fieldRetryDelaySeconds = "retry_delay_seconds"
)

// noMaximum marks a deploy field that is only checked for being a positive
// whole number, not against a configured ceiling.
const noMaximum = 0

// Smallest value a present deploy field may carry. Memory and timeout have no
// meaningful zero; a retry count or delay of 0 means "never retry" / "retry at
// once", which is the registry's default and a value a definition may state.
const (
	minLimitValue = 1
	minRetryValue = 0
)

// parseDeployInt reads one numeric deploy form field. A present field must be
// a whole number of at least minValue and at most maxValue (noMaximum = no
// ceiling); an absent field is the caller's business (it keeps the default).
func parseDeployInt(field, raw string, minValue, maxValue int) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number, got %q", field, raw)
	}
	return n, checkDeployInt(field, n, maxValue, minValue)
}

// checkDeployInt refuses a value below minValue or above maxValue.
func checkDeployInt(field string, n, maxValue, minValue int) error {
	if n < minValue {
		return fmt.Errorf("%s must be at least %d, got %d", field, minValue, n)
	}
	if maxValue != noMaximum && n > maxValue {
		return fmt.Errorf("%s must be at most %d, got %d", field, maxValue, n)
	}
	return nil
}

// validateDefinitionLimits checks the limits a definition carries, wherever
// they came from (form fields or the metadata JSON). Zero means the field was
// absent and the registry applies its default.
func (h *ServerlessHandlers) validateDefinitionLimits(def *serverless.FunctionDefinition) error {
	if err := checkDeployInt(fieldMemoryLimitMB, def.MemoryLimitMB, h.maxMemoryLimitMB, 0); err != nil {
		return err
	}
	if err := checkDeployInt(fieldTimeoutSeconds, def.TimeoutSeconds, h.maxTimeoutSeconds, 0); err != nil {
		return err
	}
	if err := checkDeployInt(fieldRetryCount, def.RetryCount, h.maxRetryCount, minRetryValue); err != nil {
		return err
	}
	return checkDeployInt(fieldRetryDelaySeconds, def.RetryDelaySeconds, noMaximum, minRetryValue)
}

// SetFunctionLimits sets the ceilings a deploy is checked against: the
// engine's configured maxima, which are what an invocation is clamped to.
func (h *ServerlessHandlers) SetFunctionLimits(maxMemoryLimitMB, maxTimeoutSeconds, maxRetryCount int) {
	h.maxMemoryLimitMB = maxMemoryLimitMB
	h.maxTimeoutSeconds = maxTimeoutSeconds
	h.maxRetryCount = maxRetryCount
}
