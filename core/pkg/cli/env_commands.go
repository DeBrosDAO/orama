package cli

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
)

// EnvList prints every configured environment, marking the active one.
func EnvList() error {
	if err := InitializeEnvironments(); err != nil {
		return clierr.Failure("failed to initialize environments: %w", err)
	}

	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return clierr.Failure("failed to load environment config: %w", err)
	}

	fmt.Printf("Available environments:\n\n")
	for _, env := range envConfig.Environments {
		active := ""
		if env.Name == envConfig.ActiveEnvironment {
			active = "  (active)"
		}
		fmt.Printf("  %s%s\n", env.Name, active)
		fmt.Printf("    Gateway: %s\n", env.GatewayURL)
		fmt.Printf("    Description: %s\n\n", env.Description)
	}
	return nil
}

// EnvCurrent prints the active environment and its gateway URL.
func EnvCurrent() error {
	if err := InitializeEnvironments(); err != nil {
		return clierr.Failure("failed to initialize environments: %w", err)
	}

	env, err := GetActiveEnvironment()
	if err != nil {
		return clierr.NotFound("no active environment: %w", err)
	}

	fmt.Printf("Current environment: %s\n", env.Name)
	fmt.Printf("   Gateway URL: %s\n", env.GatewayURL)
	fmt.Printf("   Description: %s\n", env.Description)
	return nil
}

// EnvSwitch makes the named environment active. args[0] is the name; cobra
// guarantees it is present.
func EnvSwitch(args []string) error {
	envName := args[0]

	if err := InitializeEnvironments(); err != nil {
		return clierr.Failure("failed to initialize environments: %w", err)
	}

	oldEnv, _ := GetActiveEnvironment()

	if err := SwitchEnvironment(envName); err != nil {
		return clierr.NotFound("failed to switch to %q: %w", envName, err)
	}

	newEnv, err := GetActiveEnvironment()
	if err != nil {
		return clierr.Failure("switched, but could not read the new environment back: %w", err)
	}

	if oldEnv != nil && oldEnv.Name != newEnv.Name {
		fmt.Printf("Switched environment: %s -> %s\n", oldEnv.Name, newEnv.Name)
	} else {
		fmt.Printf("Environment set to: %s\n", newEnv.Name)
	}
	fmt.Printf("   Gateway URL: %s\n", newEnv.GatewayURL)
	return nil
}

// EnvAdd registers a custom environment pointing at a gateway URL. args are
// name, gateway URL and an optional description; cobra guarantees the count.
func EnvAdd(args []string) error {
	name := args[0]
	gatewayURL := args[1]
	description := ""
	if len(args) > 2 {
		description = args[2]
	}

	if err := InitializeEnvironments(); err != nil {
		return clierr.Failure("failed to initialize environments: %w", err)
	}

	if err := AddEnvironment(name, gatewayURL, description); err != nil {
		return clierr.Failure("failed to add environment %q: %w", name, err)
	}

	fmt.Printf("Added environment: %s\n", name)
	fmt.Printf("   Gateway URL: %s\n", gatewayURL)
	if description != "" {
		fmt.Printf("   Description: %s\n", description)
	}
	return nil
}

// EnvRemove deletes a configured environment named by args[0]; cobra
// guarantees it is present.
func EnvRemove(args []string) error {
	name := args[0]

	if err := RemoveEnvironment(name); err != nil {
		return clierr.NotFound("failed to remove environment %q: %w", name, err)
	}

	fmt.Printf("Removed environment: %s\n", name)
	return nil
}
