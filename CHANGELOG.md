# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Add `ResourceWorkTimeout` client config option to configure timeouts for resource `Work()` functions.
- Add `ControllerInformTimeout` client config option to configure timeouts for controller `Inform()` functions.
- Add client `Subscribe()` function to subscribe to controller resource events.

### Fixed

- Fixed resource state and db constraint issues in the `Worker` and `Informer` methods.

### Changed

- Configured River client with `SkipUnknownJobCheck` client config option to skip job arg worker validation and remove need to instantiate a duplicate, insert-only River client.

## [ v0.1.0-alpha] - 2025-01-22

### Added

- This is the initial prerelease of Delta.
