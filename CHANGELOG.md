# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog][keepachangelog] and adheres to [Semantic Versioning][semver].

## [Unreleased]

### Added

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
