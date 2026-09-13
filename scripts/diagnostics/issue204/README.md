# Issue #204 historical reproduction

`main.go` is the original investigation fixture. It asserts that the provider-only delay bug is present and does not implement provider discovery. Its successful run on the original revision demonstrated the defect; it is not a post-fix acceptance command.

The repair is covered by `TestProviderDelay_ControlPlaneRoutesAndReportsOriginalFailure` in `internal/integration/provider_delay_test.go`, plus provider routing/retry, HTTP diagnostic, and TUI snapshot regression tests in their owning packages. All use isolated fixtures and require no real mihomo, subscription or system service.
