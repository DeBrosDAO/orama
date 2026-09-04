package cli

import (
	"fmt"
	"os"
)

// EnvList prints every configured environment, marking the active one.
func EnvList() {
	// Initialize environments if needed
	if err := InitializeEnvironments(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to initialize environments: %v\n", err)
		os.Exit(1)
	}

	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load environment config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("🌍 Available Environments:\n\n")
	for _, env := range envConfig.Environments {
		active := ""
		if env.Name == envConfig.ActiveEnvironment {
			active = " ✅ (active)"
		}
		fmt.Printf("  %s%s\n", env.Name, active)
		fmt.Printf("    Gateway: %s\n", env.GatewayURL)
		fmt.Printf("    Description: %s\n\n", env.Description)
	}
}

// EnvCurrent prints the active environment and its gateway URL.
func EnvCurrent() {
	// Initialize environments if needed
	if err := InitializeEnvironments(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to initialize environments: %v\n", err)
		os.Exit(1)
	}

	env, err := GetActiveEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to get active environment: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Current Environment: %s\n", env.Name)
	fmt.Printf("   Gateway URL: %s\n", env.GatewayURL)
	fmt.Printf("   Description: %s\n", env.Description)
}

// EnvSwitch makes the named environment active. args[0] is the name; cobra
// guarantees it is present.
func EnvSwitch(args []string) {
	envName := args[0]

	// Initialize environments if needed
	if err := InitializeEnvironments(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to initialize environments: %v\n", err)
		os.Exit(1)
	}

	// Get old environment
	oldEnv, _ := GetActiveEnvironment()

	// Switch environment
	if err := SwitchEnvironment(envName); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to switch environment: %v\n", err)
		os.Exit(1)
	}

	// Get new environment
	newEnv, err := GetActiveEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to get new environment: %v\n", err)
		os.Exit(1)
	}

	if oldEnv != nil && oldEnv.Name != newEnv.Name {
		fmt.Printf("✅ Switched environment: %s → %s\n", oldEnv.Name, newEnv.Name)
	} else {
		fmt.Printf("✅ Environment set to: %s\n", newEnv.Name)
	}
	fmt.Printf("   Gateway URL: %s\n", newEnv.GatewayURL)
}

// EnvAdd registers a custom environment pointing at a gateway URL. args are
// name, gateway URL and an optional description; cobra guarantees the count.
func EnvAdd(args []string) {
	name := args[0]
	gatewayURL := args[1]
	description := ""
	if len(args) > 2 {
		description = args[2]
	}

	// Initialize environments if needed
	if err := InitializeEnvironments(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to initialize environments: %v\n", err)
		os.Exit(1)
	}

	if err := AddEnvironment(name, gatewayURL, description); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to add environment: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Added environment: %s\n", name)
	fmt.Printf("   Gateway URL: %s\n", gatewayURL)
	if description != "" {
		fmt.Printf("   Description: %s\n", description)
	}
}

// EnvRemove deletes a configured environment named by args[0]; cobra
// guarantees it is present.
func EnvRemove(args []string) {
	name := args[0]

	if err := RemoveEnvironment(name); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to remove environment: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Removed environment: %s\n", name)
}
