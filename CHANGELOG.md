# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog][keepachangelog] and adheres to [Semantic Versioning][semver].

## [Unreleased]

### Added

### Changed

### Deprecated

### Fixed
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
