# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog][keepachangelog] and adheres to [Semantic Versioning][semver].

## [Unreleased]

### Added

### Changed

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
