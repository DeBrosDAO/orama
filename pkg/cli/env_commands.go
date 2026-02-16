package cli

import (
	"fmt"
	"os"
)

// HandleEnvCommand handles the 'env' command and its subcommands
func HandleEnvCommand(args []string) {
	if len(args) == 0 {
		showEnvHelp()
		return
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "list":
		handleEnvList()
	case "current":
		handleEnvCurrent()
	case "switch":
		handleEnvSwitch(subargs)
	case "enable":
		handleEnvEnable(subargs)
	case "add":
		handleEnvAdd(subargs)
	case "remove":
		handleEnvRemove(subargs)
	case "help":
		showEnvHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown env subcommand: %s\n", subcommand)
		showEnvHelp()
		os.Exit(1)
	}
}

func showEnvHelp() {
	fmt.Printf("🌍 Environment Management Commands\n\n")
	fmt.Printf("Usage: orama env <subcommand>\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  list       - List all available environments\n")
	fmt.Printf("  current    - Show current active environment\n")
	fmt.Printf("  switch     - Switch to a different environment\n")
	fmt.Printf("  enable     - Alias for 'switch' (e.g., 'devnet enable')\n\n")
	fmt.Printf("Available Environments:\n")
	fmt.Printf("  devnet     - Development network (https://orama-devnet.network)\n")
	fmt.Printf("  testnet    - Test network (https://orama-testnet.network)\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  orama env list\n")
	fmt.Printf("  orama env current\n")
	fmt.Printf("  orama env switch devnet\n")
	fmt.Printf("  orama env enable testnet\n")
	fmt.Printf("  orama devnet enable      # Shorthand for switch to devnet\n")
	fmt.Printf("  orama testnet enable     # Shorthand for switch to testnet\n")
}

func handleEnvList() {
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

func handleEnvCurrent() {
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

func handleEnvSwitch(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: orama env switch <environment>\n")
		fmt.Fprintf(os.Stderr, "Available: devnet, testnet\n")
		os.Exit(1)
	}

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

func handleEnvEnable(args []string) {
	// 'enable' is just an alias for 'switch'
	handleEnvSwitch(args)
}

func handleEnvAdd(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: orama env add <name> <gateway_url> [description]\n")
		fmt.Fprintf(os.Stderr, "Example: orama env add production http://dbrs.space \"Production network\"\n")
		os.Exit(1)
	}

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

	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load environment config: %v\n", err)
		os.Exit(1)
	}

	// Check if environment already exists
	for _, env := range envConfig.Environments {
		if env.Name == name {
			fmt.Fprintf(os.Stderr, "❌ Environment '%s' already exists\n", name)
			os.Exit(1)
		}
	}

	// Add new environment
	envConfig.Environments = append(envConfig.Environments, Environment{
		Name:        name,
		GatewayURL:  gatewayURL,
		Description: description,
		IsActive:    false,
	})

	if err := SaveEnvironmentConfig(envConfig); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to save environment config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Added environment: %s\n", name)
	fmt.Printf("   Gateway URL: %s\n", gatewayURL)
	if description != "" {
		fmt.Printf("   Description: %s\n", description)
	}
}

func handleEnvRemove(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: orama env remove <name>\n")
		os.Exit(1)
	}

	name := args[0]

	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load environment config: %v\n", err)
		os.Exit(1)
	}

	// Find and remove environment
	found := false
	newEnvs := make([]Environment, 0, len(envConfig.Environments))
	for _, env := range envConfig.Environments {
		if env.Name == name {
			found = true
			continue
		}
		newEnvs = append(newEnvs, env)
	}

	if !found {
		fmt.Fprintf(os.Stderr, "❌ Environment '%s' not found\n", name)
		os.Exit(1)
	}

	envConfig.Environments = newEnvs

	// If we removed the active environment, switch to devnet
	if envConfig.ActiveEnvironment == name {
		envConfig.ActiveEnvironment = "devnet"
	}

	if err := SaveEnvironmentConfig(envConfig); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to save environment config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Removed environment: %s\n", name)
}
