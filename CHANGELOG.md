# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Add `Config.Metrics` (`MetricsCollector`) for counters and histograms, with stable metric names exported as constants. Defaults to a no-op.
- Add `Client.Stats()` returning resource counts/oldest age by kind and state and unfinalized River jobs by queue, kind and state, for scrape-time gauges.
- Add `ObjectInformResult.Skipped` and the `EventCategoryObjectSkipped` event.
- Log a startup warning when the database pool is smaller than the configured worker parallelism.
- Add [monitoring](docs/monitoring.md) and [operations](docs/operations.md) guides and a Grafana dashboard.

- Add `ResourceWorkTimeout` client config option to configure timeouts for resource `Work()` functions.
- Add `ControllerInformTimeout` client config option to configure timeouts for controller `Inform()` functions.
- Add client `Subscribe()` function to subscribe to controller resource events.

### Fixed

- Stop enqueuing duplicate resource jobs: inserts are skipped when a job is already queued for the resource, and the stuck-scheduled rescue only rescues resources with no live River job.
- `Inform`/`InformTx` and `ScheduleInform` no longer panic on nil options; `ScheduleInform` jobs are routed to the controller's queue so they actually run.
- Resource jobs honor `InformOpts.MaxAttempts` (default 10) instead of River's default of 25.
- Failed resources whose River job disappeared are marked degraded instead of staying failed forever.
- The cleaner no longer hard-deletes synced resources in namespaces with a `ResourceExpiry`, no longer cleans `default` twice, and isn't capped at 1000 deletions per namespace per run.
- Maintenance queries lock and batch the rows they change, so they can't overwrite a concurrent worker's update.
- `Stop` closes subscription channels instead of leaking the subscription manager goroutine.
- The resource cancel message no longer wraps a nil error.

- Fixed resource state and db constraint issues in the `Worker` and `Informer` methods.

### Changed

- `Inform`/`InformTx` skip unchanged objects whose resource is already synced (returning `Skipped: true`). Use `Invalidate` to force a re-work.
- The `errors` column keeps the 20 most recent entries, each capped at 4 KiB.
- Expected job cancellations and snoozes are logged at INFO; informer skip logs moved to DEBUG; River logs go through `Config.Logger`.
- Bumped `golang.org/x/text` to v0.39.0 (GO-2026-5970).

- Updated `river` version to `v0.16.0`.
- Configured River client with `SkipUnknownJobCheck` client config option to skip job arg worker validation and remove need to instantiate a duplicate, insert-only River client.

## [ v0.1.0-alpha] - 2025-01-22

### Added

- This is the initial prerelease of Delta.
