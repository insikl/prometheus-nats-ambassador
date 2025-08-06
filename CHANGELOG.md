# Prometheus NATS Ambassador

All notable changes to this project will be documented in this file.

## [0.2.0] - 2025-08-06
### Added

- new CLI option `-remotewrite` for remote write URL
- new CLI option `-d` to show debug output logging

### Changed
- can omit a subscription file to properly skip
- subscription file should not be used with `-remotewrite` option
- minor version bump on features plus module upgrades

### Removed
- nil

## [0.1.3] - 2024-12-29
### Added
- new `make modup` command to update module dependancies

### Changed
- version bump on indirect modules to address security on `crypto` and `protobuf`

### Removed
- nil

## [0.1.2] - 2024-12-07
### Added
- nil

### Changed
- rename command from `nats_ambassador` to `prometheus-nats-ambassador`
- default listen port to `8181`

### Removed
- old name and references to port

## [0.1.1] - 2024-11-10
### Added
- Debian packaging files

### Changed
- fix typos
- fix HTTP header write order

### Removed
- nil

## [0.1.0] - 2024-01-10
### Added
- This CHANGELOG
- Initial README documentation
- Initial code

### Changed
- nil

### Removed
- nil
