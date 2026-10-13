package constants

// Node capacity limits used by both deployment and namespace scheduling.
const (
	MaxDeploymentsPerNode = 100
	MaxMemoryMB           = 8192 // 8GB
	MaxCPUPercent         = 400  // 400% = 4 cores
	MaxPortsPerNode       = 9800 // 10200–19999, above the index block
)
