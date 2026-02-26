# Repository Guidelines

## Project Structure & Module Organization
- `extractor/`: shared contract (`Extractor`) and sentinel errors, plus cross-strategy tests and benchmarks.
- `extractors/decoder`, `extractors/fastjson`, `extractors/gjson`, `extractors/structs`: concrete extraction strategies.
- `scripts/bench_table.awk`: post-processes benchmark output for `make bench`.
- `vendor/`: vendored third-party dependencies.

Keep strategy code in its package `extractor.go`; add package-specific tests in adjacent `_test.go` files.

## Decoder Objective (Critical)
- `extractors/decoder` is intentionally a minimal streaming scanner, not a full JSON parser.
- Primary goal: detect the first structural `"channel"` key from an `io.Reader` as early as possible and stop immediately after extracting its value.
- Preserve JSON awareness (nesting, object key/value context, escapes) to avoid false positives in large string payloads.
- Avoid whole-body buffering, AST construction, and unnecessary allocations when touching decoder logic.

## Build, Test, and Development Commands
- `make help`: list available targets.
- `make test`: run all unit tests (`go test ./...`).
- `make bench`: run benchmark suite and print a compact strategy comparison table.
- `go test ./extractors/decoder -run TestReadString`: run a focused test while iterating.
- `go test ./... -run=^$ -bench=BenchmarkExtract_ -benchmem`: run raw extractor benchmarks.

## Coding Style & Naming Conventions
- Use standard Go formatting (`gofmt`) and idioms; do not hand-format whitespace.
- Keep package names lowercase and concise (`decoder`, `fastjson`, etc.).
- Strategy implementations expose a concrete `Extractor` type with `Name()` and `Extract(io.Reader)` methods.
- Keep shared errors in `extractor/` and compare them with `errors.Is`.
- Wrap lower-level failures with context (`fmt.Errorf("decode payload: %w", err)`).

## Testing Guidelines
- Use the standard `testing` package with table-driven tests and clear case names.
- Follow existing naming patterns: `Test...` for unit tests and `BenchmarkExtract_<Scenario>_<Size>` for benchmarks.
- For behavior changes, cover at least:
  - valid extraction,
  - missing `channel`,
  - invalid `channel` type,
  - invalid JSON handling.
- Add or update benchmarks when changing parsing/performance-critical paths.

## Commit & Pull Request Guidelines
- Current branch has no commit history yet, so no established message style exists to mirror.
- Use a consistent format: `<type>: <imperative summary>` (example: `fix: validate escaped channel key matching`).
- Keep commits small and single-purpose.
- PRs should include:
  - what changed and why,
  - test evidence (`make test`; include `make bench` for performance-sensitive work),
  - any intentional behavioral differences across extractor implementations,
  - linked issue/ticket when applicable.
