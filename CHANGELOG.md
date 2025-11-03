# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog][keepachangelog] and adheres to [Semantic Versioning][semver].

## [Unreleased]

### Added

### Changed

### Deprecated

### Fixed
## [0.53.16] - 2025-11-03

### Added
\n
### Changed
- Improved the changelog generation script to prevent infinite loops when the only unpushed commit is a previous changelog update.

### Deprecated

### Removed

### Fixed
\n
## [0.53.15] - 2025-11-03

### Added
\n
### Changed
- Improved the pre-push git hook to automatically commit updated changelog and Makefile after generation.
- Updated the changelog generation script to load the OpenRouter API key from the .env file or environment variables for better security.
- Modified the pre-push hook to read user confirmation from /dev/tty for better compatibility.
- Updated the bootstrap peer logic to prioritize the DEBROS_BOOTSTRAP_PEERS environment variable for easier configuration.
- Improved the gateway's private host check to correctly handle IPv6 addresses with or without brackets and ports.

### Deprecated

### Removed

### Fixed
\n
## [0.53.15] - 2025-11-03

### Added
\n
### Changed
- Improved the pre-push git hook to automatically commit updated changelog and Makefile after generation.
- Updated the changelog generation script to load the OpenRouter API key from the .env file or environment variables for better security.
- Modified the pre-push hook to read user confirmation from /dev/tty for better compatibility.
- Updated the bootstrap peer logic to prioritize the DEBROS_BOOTSTRAP_PEERS environment variable for easier configuration.
- Improved the gateway's private host check to correctly handle IPv6 addresses with or without brackets and ports.

### Deprecated

### Removed

### Fixed
\n
## [0.53.14] - 2025-11-03

### Added
- Added a new `install-hooks` target to the Makefile to easily set up git hooks.
- Added a script (`scripts/install-hooks.sh`) to copy git hooks from `.githooks` to `.git/hooks`.

### Changed
- Improved the pre-push git hook to automatically commit the updated `CHANGELOG.md` and `Makefile` after generating the changelog.
- Updated the changelog generation script (`scripts/update_changelog.sh`) to load the OpenRouter API key from the `.env` file or environment variables, improving security and configuration.
- Modified the pre-push hook to read user confirmation from `/dev/tty` for better compatibility in various terminal environments.
- Updated the bootstrap peer logic to check the `DEBROS_BOOTSTRAP_PEERS` environment variable first, allowing easier configuration override.
- Improved the gateway's private host check to correctly handle IPv6 addresses with or without brackets and ports.

### Deprecated

### Removed

### Fixed
\n
## [0.53.14] - 2025-11-03

### Added
- Added a new `install-hooks` target to the Makefile to easily set up git hooks.
- Added a script (`scripts/install-hooks.sh`) to copy git hooks from `.githooks` to `.git/hooks`.

### Changed
- Improved the pre-push git hook to automatically commit the updated `CHANGELOG.md` and `Makefile` after generating the changelog.
- Updated the changelog generation script (`scripts/update_changelog.sh`) to load the OpenRouter API key from the `.env` file or environment variables, improving security and configuration.
- Modified the pre-push hook to read user confirmation from `/dev/tty` for better compatibility in various terminal environments.

### Deprecated

### Removed

### Fixed
\n

## [0.53.8] - 2025-10-31

### Added

- **HTTPS/ACME Support**: Gateway now supports automatic HTTPS with Let's Encrypt certificates via ACME
  - Interactive domain configuration during `network-cli setup` command
  - Automatic port availability checking for ports 80 and 443 before enabling HTTPS
  - DNS resolution verification to ensure domain points to the server IP
  - TLS certificate cache directory management (`~/.debros/tls-cache`)
  - Gateway automatically serves HTTP (port 80) for ACME challenges and HTTPS (port 443) for traffic
  - New gateway config fields: `enable_https`, `domain_name`, `tls_cache_dir`
- **Domain Validation**: Added domain name validation and DNS verification helpers in setup CLI
- **Port Checking**: Added port availability checking utilities to detect conflicts before HTTPS setup

### Changed

- Updated `generateGatewayConfigDirect` to include HTTPS configuration fields
- Enhanced gateway config parsing to support HTTPS settings with validation
- Modified gateway startup to handle both HTTP-only and HTTPS+ACME modes
- Gateway now automatically manages ACME certificate acquisition and renewal

### Fixed

- Improved error handling during HTTPS setup with clear messaging when ports are unavailable
- Enhanced DNS verification flow with better user feedback during setup

## [0.53.0] - 2025-10-31

### Added

- Discovery manager now tracks failed peer-exchange attempts to suppress repeated warnings while peers negotiate supported protocols.

### Changed

- Scoped logging throughout `cluster_discovery`, `rqlite`, and `discovery` packages so logs carry component tags and keep verbose output at debug level.
- Refactored `ClusterDiscoveryService` membership handling: metadata updates happen under lock, `peers.json` is written outside the lock, self-health is skipped, and change detection is centralized in `computeMembershipChangesLocked`.
- Reworked `RQLiteManager.Start` into helper functions (`prepareDataDir`, `launchProcess`, `waitForReadyAndConnect`, `establishLeadershipOrJoin`) with clearer logging, better error handling, and exponential backoff while waiting for leadership.
- `validateNodeID` now treats empty membership results as transitional states, logging at debug level instead of warning to avoid noisy startups.

### Fixed

- Eliminated spurious `peers.json` churn and node-ID mismatch warnings during cluster formation by aligning IDs with raft addresses and tightening discovery logging.

## [0.52.15]

### Added

- Added Base64 encoding for the response body in the anonProxyHandler to prevent corruption of binary data when returned in JSON format.

### Changed

- **GoReleaser**: Updated to build only `network-cli` binary (v0.52.2+)
  - Other binaries (node, gateway, identity) now installed via `network-cli setup`
  - Cleaner, smaller release packages
  - Resolves archive mismatch errors
- **GitHub Actions**: Updated artifact actions from v3 to v4 (deprecated versions)

### Deprecated

### Fixed

- Fixed install script to be more clear and bug fixing

## [0.52.1] - 2025-10-26

### Added

- **CLI Refactor**: Modularized monolithic CLI into `pkg/cli/` package structure for better maintainability
  - New `environment.go`: Multi-environment management system (local, devnet, testnet)
  - New `env_commands.go`: Environment switching commands (`env list`, `env switch`, `devnet enable`, `testnet enable`)
  - New `setup.go`: Interactive VPS installation command (`network-cli setup`) that replaces bash install script
  - New `service.go`: Systemd service management commands (`service start|stop|restart|status|logs`)
  - New `auth_commands.go`, `config_commands.go`, `basic_commands.go`: Refactored commands into modular pkg/cli
- **Release Pipeline**: Complete automated release infrastructure via `.goreleaser.yaml` and GitHub Actions
  - Multi-platform binary builds (Linux/macOS, amd64/arm64)
  - Automatic GitHub Release creation with changelog and artifacts
  - Semantic versioning support with pre-release handling
- **Environment Configuration**: Multi-environment switching system
  - Default environments: local (http://localhost:6001), devnet (https://devnet.debros.network), testnet (https://testnet.debros.network)
  - Stored in `~/.debros/environments.json`
  - CLI auto-uses active environment for authentication and operations
- **Comprehensive Documentation**
  - `.cursor/RELEASES.md`: Overview and quick start
  - `.cursor/goreleaser-guide.md`: Detailed distribution guide
  - `.cursor/release-checklist.md`: Quick reference

### Changed

- **CLI Refactoring**: `cmd/cli/main.go` reduced from 1340 → 180 lines (thin router pattern)
  - All business logic moved to modular `pkg/cli/` functions
  - Easier to test, maintain, and extend individual commands
- **Installation**: `scripts/install-debros-network.sh` now APT-ready with fallback to source build
- **Setup Process**: Consolidated all installation logic into `network-cli setup` command
  - Single unified installation regardless of installation method
  - Interactive user experience with clear progress indicators

### Removed

## [0.51.9] - 2025-10-25

### Added

- One-command `make dev` target to start full development stack (bootstrap + node2 + node3 + gateway in background)
- New `network-cli config init` (no --type) generates complete development stack with all configs and identities
- Full stack initialization with auto-generated peer identities for bootstrap and all nodes
- Explicit control over LibP2P listen addresses for better localhost/development support
- Production/development mode detection for NAT services (disabled for localhost, enabled for production)
- Process management with .dev/pids directory for background process tracking
- Centralized logging to ~/.debros/logs/ for all network services

### Changed

- Simplified Makefile: removed legacy dev commands, replaced with unified `make dev` target
- Updated README with clearer getting started instructions (single `make dev` command)
- Simplified `network-cli config init` behavior: defaults to generating full stack instead of single node
- `network-cli config init` now handles bootstrap peer discovery and join addresses automatically
- LibP2P configuration: removed always-on NAT services for development environments
- Code formatting in pkg/node/node.go (indentation fixes in bootstrapPeerSource)

### Deprecated

### Removed

- Removed legacy Makefile targets: run-example, show-bootstrap, run-cli, cli-health, cli-peers, cli-status, cli-storage-test, cli-pubsub-test
- Removed verbose dev-setup, dev-cluster, and old dev workflow targets

### Fixed

- Fixed indentation in bootstrapPeerSource function for consistency
- Fixed gateway.yaml generation with correct YAML indentation for bootstrap_peers
- Fixed script for running and added gateway running as well

### Security

## [0.51.6] - 2025-10-24

### Added

- LibP2P added support over NAT

### Changed

### Deprecated

### Removed

### Fixed

## [0.51.5] - 2025-10-24

### Added

- Added validation for yaml files
- Added authenticaiton command on cli

### Changed

- Updated readme
- Where we read .yaml files from and where data is saved to ~/.debros

### Deprecated

### Removed

### Fixed

- Regular nodes rqlite not starting

## [0.51.2] - 2025-09-26

### Added

### Changed

- Enhance gateway configuration by adding RQLiteDSN support and updating default connection settings. Updated config parsing to include RQLiteDSN from YAML and environment variables. Changed default RQLite connection URL from port 4001 to 5001.
- Update CHANGELOG.md for version 0.51.2, enhance API key extraction to support query parameters, and implement internal auth context in status and storage handlers.

## [0.51.1] - 2025-09-26

### Added

### Changed

- Changed the configuration file for run-node3 to use node3.yaml.
- Modified select_data_dir function to require a hasConfigFile parameter and added error handling for missing configuration.
- Updated main function to pass the config path to select_data_dir.
- Introduced a peer exchange protocol in the discovery package, allowing nodes to request and exchange peer information.
- Refactored peer discovery logic in the node package to utilize the new discovery manager for active peer exchange.
- Cleaned up unused code related to previous peer discovery methods.

### Deprecated

### Removed

### Fixed

## [0.50.0] - 2025-09-23

### Added

### Changed

### Deprecated

### Removed

### Fixed

- Fixed wrong URL /v1/db to /v1/rqlite

### Security

## [0.50.0] - 2025-09-23

### Added

- Created new rqlite folder
- Created rqlite adapter, client, gateway, migrations and rqlite init
- Created namespace_helpers on gateway
- Created new rqlite implementation

### Changed

- Updated node.go to support new rqlite architecture
- Updated readme

### Deprecated

### Removed

- Removed old storage folder
- Removed old pkg/gatway storage and migrated to new rqlite

### Fixed

### Security

## [0.44.0] - 2025-09-22

### Added

- Added gateway.yaml file for gateway default configurations

### Changed

- Updated readme to include all options for .yaml files

### Deprecated

### Removed

- Removed unused command setup-production-security.sh
- Removed anyone proxy from libp2p proxy

### Fixed

### Security

## [0.43.6] - 2025-09-20

### Added

- Added Gateway port on install-debros-network.sh
- Added default bootstrap peers on config.go

### Changed

- Updated Gateway port from 8080/8005 to 6001

### Deprecated

### Removed

### Fixed

### Security

## [0.43.4] - 2025-09-18

### Added

- Added extra comments on main.go
- Remove backoff_test.go and associated backoff tests
- Created node_test, write tests for CalculateNextBackoff, AddJitter, GetPeerId, LoadOrCreateIdentity, hasBootstrapConnections

### Changed

- replaced git.debros.io with github.com

### Deprecated

### Removed

### Fixed

### Security

## [0.43.3] - 2025-09-15

### Added

- User authentication module with OAuth2 support.

### Changed

- Make file version to 0.43.2

### Deprecated

### Removed

- Removed cli, network-cli binaries from project
- Removed AI_CONTEXT.md
- Removed Network.md
- Removed unused log from monitoring.go

### Fixed

- Resolved race condition when saving settings.

### Security

_Initial release._

[keepachangelog]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/spec/v2.0.0.html
