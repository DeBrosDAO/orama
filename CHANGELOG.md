# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog][keepachangelog] and adheres to [Semantic Versioning][semver].

## [Unreleased]

### Added

### Changed

### Deprecated

### Fixed
## [0.69.8] - 2025-11-12

### Added
- Improved `dbn prod start` to automatically unmask and re-enable services if they were previously masked or disabled.
- Added automatic discovery and configuration of all IPFS Cluster peers during runtime to improve cluster connectivity.

### Changed
- Enhanced `dbn prod start` and `dbn prod stop` reliability by adding service state resets, retries, and ensuring services are disabled when stopped.
- Filtered peer exchange addresses in LibP2P discovery to only include the standard LibP2P port (4001), preventing exposure of internal service ports.

### Deprecated

### Removed

### Fixed
- Improved IPFS Cluster bootstrap configuration repair logic to automatically infer and update bootstrap peer addresses if the bootstrap node is available.

## [0.69.7] - 2025-11-12

### Added
\n
### Changed
- Improved logic for determining Olric server addresses during configuration generation, especially for bootstrap and non-bootstrap nodes.
- Enhanced IPFS cluster configuration to correctly handle IPv6 addresses when updating bootstrap peers.

### Deprecated

### Removed

### Fixed
\n
## [0.69.6] - 2025-11-12

### Added
- Improved production service health checks and port availability validation during install, upgrade, start, and restart commands.
- Added service aliases (node, ipfs, cluster, gateway, olric) to `dbn prod logs` command for easier log viewing.

### Changed
- Updated node configuration logic to correctly advertise public IP addresses in multiaddrs (for P2P discovery) and RQLite addresses, improving connectivity for nodes behind NAT/firewalls.
- Enhanced `dbn prod install` and `dbn prod upgrade` to automatically detect and preserve existing VPS IP, domain, and cluster join information.
- Improved RQLite cluster discovery to automatically replace localhost/loopback addresses with the actual public IP when exchanging metadata between peers.
- Updated `dbn prod install` to require `--vps-ip` for all node types (bootstrap and regular) for proper network configuration.
- Improved error handling and robustness in the installation script when fetching the latest release from GitHub.

### Deprecated

### Removed

### Fixed
- Fixed an issue where the RQLite process would wait indefinitely for a join target; now uses a 5-minute timeout.
- Corrected the location of the gateway configuration file reference in the README.

## [0.69.5] - 2025-11-11

### Added
\n
### Changed
- Moved the default location for `gateway.yaml` configuration file from `configs/` to the new `data/` directory for better organization.
- Updated configuration path logic to search for `gateway.yaml` in the new `data/` directory first.

### Deprecated

### Removed

### Fixed
\n
## [0.69.4] - 2025-11-11

### Added
\n
### Changed
- RQLite database management is now integrated directly into the main node process, removing separate RQLite systemd services (debros-rqlite-*).
- Improved log file provisioning to only create necessary log files based on the node type being installed (bootstrap or node).

### Deprecated

### Removed

### Fixed
\n
## [0.69.3] - 2025-11-11

### Added
- Added `--ignore-resource-checks` flag to the install command to skip disk, RAM, and CPU prerequisite validation.

### Changed
\n
### Deprecated

### Removed

### Fixed
\n
## [0.69.2] - 2025-11-11

### Added
- Added `--no-pull` flag to `dbn prod upgrade` to skip git repository updates and use existing source code.

### Changed
- Removed deprecated environment management commands (`env`, `devnet`, `testnet`, `local`).
- Removed deprecated network commands (`health`, `peers`, `status`, `peer-id`, `connect`, `query`, `pubsub`) from the main CLI interface.

### Deprecated

### Removed

### Fixed
\n
## [0.69.1] - 2025-11-11

### Added
- Added automatic service stopping before binary upgrades during the `prod upgrade` process to ensure a clean update.
- Added logic to preserve existing configuration settings (like `bootstrap_peers`, `domain`, and `rqlite_join_address`) when regenerating configurations during `prod upgrade`.

### Changed
- Improved the `prod upgrade` process to be more robust by preserving critical configuration details and gracefully stopping services.

### Deprecated

### Removed

### Fixed
\n
## [0.69.0] - 2025-11-11

### Added
- Added comprehensive documentation for setting up HTTPS using a domain name, including configuration steps for both installation and existing setups.
- Added the `--force` flag to the `install` command for reconfiguring all settings.
- Added new log targets (`ipfs-cluster`, `rqlite`, `olric`) and improved the `dbn prod logs` command documentation.

### Changed
- Improved the IPFS Cluster configuration logic to ensure the cluster secret and IPFS API port are correctly synchronized during updates.
- Refined the directory structure creation process to ensure node-specific data directories are created only when initializing services.

### Deprecated

### Removed

### Fixed
\n
## [0.68.1] - 2025-11-11

### Added
- Pre-create log files during setup to ensure correct permissions for systemd logging.

### Changed
- Improved binary installation process to handle copying files individually, preventing potential shell wildcard issues.
- Enhanced ownership fixing logic during installation to ensure all files created by root (especially during service initialization) are correctly owned by the 'debros' user.

### Deprecated

### Removed

### Fixed
\n
## [0.68.0] - 2025-11-11

### Added
- Added comprehensive documentation for production deployment, including installation, upgrade, service management, and troubleshooting.
- Added new CLI commands (`dbn prod start`, `dbn prod stop`, `dbn prod restart`) for convenient management of production systemd services.

### Changed
- Updated IPFS configuration during production installation to use port 4501 for the API (to avoid conflicts with RQLite on port 5001) and port 8080 for the Gateway.

### Deprecated

### Removed

### Fixed
- Ensured that IPFS configuration automatically disables AutoConf when a private swarm key is present during installation and upgrade, preventing startup errors.

## [0.67.7] - 2025-11-11

### Added
- Added support for specifying the Git branch (main or nightly) during `prod install` and `prod upgrade`.
- The chosen branch is now saved and automatically used for future upgrades unless explicitly overridden.

### Changed
- Updated help messages and examples for production commands to include branch options.

### Deprecated

### Removed

### Fixed
\n
## [0.67.6] - 2025-11-11

### Added
\n
### Changed
- The binary installer now updates the source repository if it already exists, instead of only cloning it if missing.

### Deprecated

### Removed

### Fixed
- Resolved an issue where disabling AutoConf in the IPFS repository could leave 'auto' placeholders in the config, causing startup errors.

## [0.67.5] - 2025-11-11

### Added
- Added `--restart` option to `dbn prod upgrade` to automatically restart services after upgrade.
- The gateway now supports an optional `--config` flag to specify the configuration file path.

### Changed
- Improved `dbn prod upgrade` process to better handle existing installations, including detecting node type and ensuring configurations are updated to the latest format.
- Configuration loading logic for `node` and `gateway` commands now correctly handles absolute paths passed via command line or systemd.

### Deprecated

### Removed

### Fixed
- Fixed an issue during production upgrades where IPFS repositories in private swarms might fail to start due to `AutoConf` not being disabled.

## [0.67.4] - 2025-11-11

### Added
\n
### Changed
- Improved configuration file loading logic to support absolute paths for config files.
- Updated IPFS Cluster initialization during setup to run `ipfs-cluster-service init` and automatically configure the cluster secret.
- IPFS repositories initialized with a private swarm key will now automatically disable AutoConf.

### Deprecated

### Removed

### Fixed
- Fixed configuration path resolution to correctly check for config files in both the legacy (`~/.debros/`) and production (`~/.debros/configs/`) directories.

## [0.67.3] - 2025-11-11

### Added
\n
### Changed
- Improved reliability of IPFS (Kubo) installation by switching from a single install script to the official step-by-step download and extraction process.
- Updated IPFS (Kubo) installation to use version v0.38.2.
- Enhanced binary installation routines (RQLite, IPFS, Go) to ensure the installed binaries are immediately available in the current process's PATH.

### Deprecated

### Removed

### Fixed
- Fixed potential installation failures for RQLite by adding error checking to the binary copy command.

## [0.67.2] - 2025-11-11

### Added
- Added a new utility function to reliably resolve the full path of required external binaries (like ipfs, rqlited, etc.).

### Changed
- Improved service initialization by validating the availability and path of all required external binaries before creating systemd service units.
- Updated systemd service generation logic to use the resolved, fully-qualified paths for external binaries instead of relying on hardcoded paths.

### Deprecated

### Removed

### Fixed
- Changed IPFS initialization from a warning to a fatal error if the repo fails to initialize, ensuring setup stops on critical failures.

## [0.67.1] - 2025-11-11

### Added
\n
### Changed
- Improved disk space check logic to correctly check the parent directory if the specified path does not exist.

### Deprecated

### Removed

### Fixed
- Fixed an issue in the installation script where the extracted CLI binary might be named 'dbn' instead of 'network-cli', ensuring successful installation regardless of the extracted filename.

## [0.67.0] - 2025-11-11

### Added
- Added support for joining a cluster as a secondary bootstrap node using the new `--bootstrap-join` flag.
- Added a new flag `--vps-ip` to specify the public IP address for non-bootstrap nodes, which is now required for cluster joining.

### Changed
- Updated the installation script to correctly download and install the CLI binary from the GitHub release archive.
- Improved RQLite service configuration to correctly use the public IP address (`--vps-ip`) for advertising its raft and HTTP addresses.

### Deprecated

### Removed

### Fixed
- Fixed an issue where non-bootstrap nodes could be installed without specifying the required `--vps-ip`.

## [0.67.0] - 2025-11-11

### Added
- Added support for joining a cluster as a secondary bootstrap node using the new `--bootstrap-join` flag.
- Added a new flag `--vps-ip` to specify the public IP address for non-bootstrap nodes, which is now required for cluster joining.

### Changed
- Updated the installation script to correctly download and install the CLI binary from the GitHub release archive.
- Improved RQLite service configuration to correctly use the public IP address (`--vps-ip`) for advertising its raft and HTTP addresses.

### Deprecated

### Removed

### Fixed
- Fixed an issue where non-bootstrap nodes could be installed without specifying the required `--vps-ip`.

## [0.66.1] - 2025-11-11

### Added
\n
### Changed
- Allow bootstrap nodes to optionally define a join address to synchronize with another bootstrap cluster.

### Deprecated

### Removed

### Fixed
\n
## [0.66.0] - 2025-11-11

### Added
- Pre-installation checks for minimum system resources (10GB disk space, 2GB RAM, 2 CPU cores) are now performed during setup.
- All systemd services (IPFS, RQLite, Olric, Node, Gateway) now log directly to dedicated files in the logs directory instead of using the system journal.

### Changed
- Improved logging instructions in the setup completion message to reference the new dedicated log files.

### Deprecated

### Removed

### Fixed
\n
## [0.65.0] - 2025-11-11

### Added
- Expanded the local development environment (`dbn dev up`) from 3 nodes to 5 nodes (2 bootstraps and 3 regular nodes) for better testing of cluster resilience and quorum.
- Added a new `bootstrap2` node configuration and service to the development topology.

### Changed
- Updated the `dbn dev up` command to configure and start all 5 nodes and associated services (IPFS, RQLite, IPFS Cluster).
- Modified RQLite and LibP2P health checks in the development environment to require a quorum of 3 out of 5 nodes.
- Refactored development environment configuration logic using a new `Topology` structure for easier management of node ports and addresses.

### Deprecated

### Removed

### Fixed
- Ensured that secondary bootstrap nodes can correctly join the primary RQLite cluster in the development environment.

## [0.64.1] - 2025-11-10

### Added
\n
### Changed
- Improved the accuracy of the Raft log index reporting by falling back to reading persisted snapshot metadata from disk if the running RQLite instance is not yet reachable or reports a zero index.

### Deprecated

### Removed

### Fixed
\n
## [0.64.0] - 2025-11-10

### Added
- Comprehensive End-to-End (E2E) test suite for Gateway API endpoints (Cache, RQLite, Storage, Network, Auth).
- New E2E tests for concurrent operations and TTL expiry in the distributed cache.
- New E2E tests for LibP2P peer connectivity and discovery.

### Changed
- Improved Gateway E2E test configuration: automatically discovers Gateway URL and API Key from local `~/.debros` configuration files, removing the need for environment variables.
- The `/v1/network/peers` endpoint now returns a flattened list of multiaddresses for all connected peers.
- Improved robustness of Cache API handlers to correctly identify and return 404 (Not Found) errors when keys are missing, even when wrapped by underlying library errors.
- The RQLite transaction handler now supports the legacy `statements` array format in addition to the `ops` array format for easier use.
- The RQLite schema endpoint now returns tables under the `tables` key instead of `objects`.

### Deprecated

### Removed

### Fixed
- Corrected IPFS Add operation to return the actual file size (byte count) instead of the DAG size in the response.

## [0.63.3] - 2025-11-10

### Added
\n
### Changed
- Improved RQLite cluster stability by automatically clearing stale Raft state on startup if peers have a higher log index, allowing the node to join cleanly.

### Deprecated

### Removed

### Fixed
\n
## [0.63.2] - 2025-11-10

### Added
\n
### Changed
- Improved process termination logic in development environments to ensure child processes are also killed.
- Enhanced the `dev-kill-all.sh` script to reliably kill all processes using development ports, including orphaned processes and their children.

### Deprecated

### Removed

### Fixed
\n
## [0.63.1] - 2025-11-10

### Added
\n
### Changed
- Increased the default minimum cluster size for database environments from 1 to 3.

### Deprecated

### Removed

### Fixed
- Prevented unnecessary cluster recovery attempts when a node starts up as the first node (fresh bootstrap).

## [0.63.0] - 2025-11-10

### Added
- Added a new `kill` command to the Makefile for forcefully shutting down all development processes.
- Introduced a new `stop` command in the Makefile for graceful shutdown of development processes.

### Changed
- The `kill` command now performs a graceful shutdown attempt followed by a force kill of any lingering processes and verifies that development ports are free.

### Deprecated

### Removed

### Fixed
\n
## [0.62.0] - 2025-11-10

### Added
- The `prod status` command now correctly checks for both 'bootstrap' and 'node' service variants.

### Changed
- The production installation process now generates secrets (like the cluster secret and peer ID) before initializing services. This ensures all necessary secrets are available when services start.
- The `prod install` command now displays the actual Peer ID upon completion instead of a placeholder.

### Deprecated

### Removed

### Fixed
- Fixed an issue where IPFS Cluster initialization was using a hardcoded configuration file instead of relying on the standard `ipfs-cluster-service init` process.

## [0.61.0] - 2025-11-10

### Added
- Introduced a new simplified authentication flow (`dbn auth login`) that allows users to generate an API key directly from a wallet address without signature verification (for development/testing purposes).
- Added a new `PRODUCTION_INSTALL.md` guide for production deployment using the `dbn prod` command suite.

### Changed
- Renamed the primary CLI binary from `network-cli` to `dbn` across all configurations, documentation, and source code.
- Refactored the IPFS configuration logic in the development environment to directly modify the IPFS config file instead of relying on shell commands, improving stability.
- Improved the IPFS Cluster peer count logic to correctly handle NDJSON streaming responses from the `/peers` endpoint.
- Enhanced RQLite connection logic to retry connecting to the database if the store is not yet open, particularly for joining nodes during recovery, improving cluster stability.

### Deprecated

### Removed

### Fixed
\n

## [0.60.1] - 2025-11-09

### Added

- Improved IPFS Cluster startup logic in development environment to ensure proper peer discovery and configuration.

### Changed

- Refactored IPFS Cluster initialization in the development environment to use a multi-phase startup (bootstrap first, then followers) and explicitly clean stale cluster state (pebble, peerstore) before initialization.

### Deprecated

### Removed

### Fixed

- Fixed an issue where IPFS Cluster nodes in the development environment might fail to join due to incorrect bootstrap configuration or stale state.

## [0.60.0] - 2025-11-09

### Added

- Introduced comprehensive `dbn dev` commands for managing the local development environment (start, stop, status, logs).
- Added `dbn prod` commands for streamlined production installation, upgrade, and service management on Linux systems (requires root).

### Changed

- Refactored `Makefile` targets (`dev` and `kill`) to use the new `dbn dev up` and `dbn dev down` commands, significantly simplifying the development workflow.
- Removed deprecated `dbn config`, `dbn setup`, `dbn service`, and `dbn rqlite` commands, consolidating functionality under `dev` and `prod`.

### Deprecated

### Removed

### Fixed

\n

## [0.59.2] - 2025-11-08

### Added

- Added health checks to the installation script to verify the gateway and node services are running after setup or upgrade.
- The installation script now attempts to verify the downloaded binary using checksums.txt if available.
- Added checks in the CLI setup to ensure systemd is available before attempting to create service files.

### Changed

- Improved the installation script to detect existing installations, stop services before upgrading, and restart them afterward to minimize downtime.
- Enhanced the CLI setup process by detecting the VPS IP address earlier and improving validation feedback for cluster secrets and swarm keys.
- Modified directory setup to log warnings instead of exiting if `chown` fails, providing manual instructions for fixing ownership issues.
- Improved the HTTPS configuration flow to check for port 80/443 availability before prompting for a domain name.

### Deprecated

### Removed

### Fixed

\n

## [0.59.1] - 2025-11-08

### Added

\n

### Changed

- Improved interactive setup to prompt for existing IPFS Cluster secret and Swarm key, allowing easier joining of existing private networks.
- Updated default IPFS API URL in configuration files from `http://localhost:9105` to the standard `http://localhost:5001`.
- Updated systemd service files (debros-ipfs.service and debros-ipfs-cluster.service) to correctly determine and use the IPFS and Cluster repository paths.

### Deprecated

### Removed

### Fixed

\n

## [0.59.0] - 2025-11-08

### Added

- Added support for asynchronous pinning of uploaded files, improving upload speed.
- Added an optional `pin` flag to the storage upload endpoint to control whether content is pinned (defaults to true).

### Changed

- Improved handling of IPFS Cluster responses during the Add operation to correctly process streaming NDJSON output.

### Deprecated

### Removed

### Fixed

\n

## [0.58.0] - 2025-11-07

### Added

- Added default configuration for IPFS Cluster and IPFS API settings in node and gateway configurations.
- Added `ipfs` configuration section to node configuration, including settings for cluster API URL, replication factor, and encryption.

### Changed

- Improved error logging for cache operations in the Gateway.

### Deprecated

### Removed

### Fixed

\n

## [0.57.0] - 2025-11-07

### Added

- Added a new endpoint `/v1/cache/mget` to retrieve multiple keys from the distributed cache in a single request.

### Changed

- Improved API key extraction logic to prioritize the `X-API-Key` header and better handle different authorization schemes (Bearer, ApiKey) while avoiding confusion with JWTs.
- Refactored cache retrieval logic to use a dedicated function for decoding values from the distributed cache.

### Deprecated

### Removed

### Fixed

\n

## [0.56.0] - 2025-11-05

### Added

- Added IPFS storage endpoints to the Gateway for content upload, pinning, status, retrieval, and unpinning.
- Introduced `StorageClient` interface and implementation in the Go client library for interacting with the new IPFS storage endpoints.
- Added support for automatically starting IPFS daemon, IPFS Cluster daemon, and Olric cache server in the `dev` environment setup.

### Changed

- Updated Gateway configuration to include settings for IPFS Cluster API URL, IPFS API URL, timeout, and replication factor.
- Refactored Olric configuration generation to use a simpler, local-environment focused setup.
- Improved IPFS content retrieval (`Get`) to fall back to the IPFS Gateway (port 8080) if the IPFS API (port 5001) returns a 404.

### Deprecated

### Removed

### Fixed

## [0.54.0] - 2025-11-03

### Added

- Integrated Olric distributed cache for high-speed key-value storage and caching.
- Added new HTTP Gateway endpoints for cache operations (GET, PUT, DELETE, SCAN) via `/v1/cache/`.
- Added `olric_servers` and `olric_timeout` configuration options to the Gateway.
- Updated the automated installation script (`install-debros-network.sh`) to include Olric installation, configuration, and firewall rules (ports 3320, 3322).

### Changed

- Refactored README for better clarity and organization, focusing on quick start and core features.

### Deprecated

### Removed

### Fixed

\n

## [0.53.18] - 2025-11-03

### Added

\n

### Changed

- Increased the connection timeout during peer discovery from 15 seconds to 20 seconds to improve connection reliability.
- Removed unnecessary debug logging related to filtering out ephemeral port addresses during peer exchange.

### Deprecated

### Removed

### Fixed

\n

## [0.53.17] - 2025-11-03

### Added

- Added a new Git `pre-commit` hook to automatically update the changelog and version before committing, ensuring version consistency.

### Changed

- Refactored the `update_changelog.sh` script to support different execution contexts (pre-commit vs. pre-push), allowing it to analyze only staged changes during commit.
- The Git `pre-push` hook was simplified by removing the changelog update logic, which is now handled by the `pre-commit` hook.

### Deprecated

### Removed

### Fixed

\n

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
  - Interactive domain configuration during `dbn setup` command
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

- **GoReleaser**: Updated to build only `dbn` binary (v0.52.2+)
  - Other binaries (node, gateway, identity) now installed via `dbn setup`
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
  - New `setup.go`: Interactive VPS installation command (`dbn setup`) that replaces bash install script
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
- **Setup Process**: Consolidated all installation logic into `dbn setup` command
  - Single unified installation regardless of installation method
  - Interactive user experience with clear progress indicators

### Removed

## [0.51.9] - 2025-10-25

### Added

- One-command `make dev` target to start full development stack (bootstrap + node2 + node3 + gateway in background)
- New `dbn config init` (no --type) generates complete development stack with all configs and identities
- Full stack initialization with auto-generated peer identities for bootstrap and all nodes
- Explicit control over LibP2P listen addresses for better localhost/development support
- Production/development mode detection for NAT services (disabled for localhost, enabled for production)
- Process management with .dev/pids directory for background process tracking
- Centralized logging to ~/.debros/logs/ for all network services

### Changed

- Simplified Makefile: removed legacy dev commands, replaced with unified `make dev` target
- Updated README with clearer getting started instructions (single `make dev` command)
- Simplified `dbn config init` behavior: defaults to generating full stack instead of single node
- `dbn config init` now handles bootstrap peer discovery and join addresses automatically
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

- Removed cli, dbn binaries from project
- Removed AI_CONTEXT.md
- Removed Network.md
- Removed unused log from monitoring.go

### Fixed

- Resolved race condition when saving settings.

### Security

_Initial release._

[keepachangelog]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/spec/v2.0.0.html
