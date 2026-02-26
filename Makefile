SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

.DEFAULT_GOAL := help

BENCH_ARGS := ./extractor -run=^$$ -bench=BenchmarkExtract_ -benchmem
BENCH_ARGS_DECODER_ONLY := ./extractor -run=^$$ -bench='BenchmarkExtract_.*/decoder' -benchmem
PROFILE_BENCH := BenchmarkExtract_ChannelLate_Large/decoder$$

BASELINE_FILE_NAME := baseline.txt
BENCH_HISTORY_FOLDER := bench-history
BASELINE_HISTORY_PATH := $(BENCH_HISTORY_FOLDER)/$(BASELINE_FILE_NAME)

.PHONY: help test bench profile compare compare-update-baseline

help:
	@echo "Targets:"
	@echo "  make test          Run all unit tests"
	@echo "  make bench         Run benchmarks and print a compact decoder/struct/gjson/fastjson summary"
	@echo "  make compare       Compare decoder-only benchmark results with baseline"
	@echo "  make compare-update-baseline  Refresh baseline benchmark file"
	@echo "  make profile       Collect CPU+MEM profiles and serve two pprof UIs with flame graphs"

test:
	go test ./...

bench:
	@tmp_file="$$(mktemp)"; \
	trap 'rm -f "$$tmp_file"' EXIT; \
	go test $(BENCH_ARGS) | tee "$$tmp_file"; \
	out="$$(cat "$$tmp_file")"; \
	use_color=1; \
	if [ -n "$$NO_COLOR" ] || [ ! -t 1 ]; then use_color=0; fi; \
	echo ""; \
	echo "=== Summary by metric (decoder | struct | gjson | fastjson, lower is better) ==="; \
	printf '%s\n' "$$out" | awk -v USE_COLOR="$$use_color" -f scripts/bench_table.awk

compare-update-baseline:
	@mkdir -p $(BENCH_HISTORY_FOLDER)
	go test $(BENCH_ARGS) | tee $(BASELINE_HISTORY_PATH)

compare:
	@tmp_file="$$(mktemp)"; \
	trap 'rm -f "$$tmp_file"' EXIT; \
	go test $(BENCH_ARGS_DECODER_ONLY) | tee "$$tmp_file"; \
	benchstat history/baseline.txt "$$tmp_file"

profile:
	@mkdir -p profiles
	go test ./extractor -run='^$$' -bench='$(PROFILE_BENCH)' -benchmem -cpuprofile=profiles/decoder.cpu.out -memprofile=profiles/decoder.mem.out
	@cpu_port=18081; \
	mem_port=18082; \
	echo ""; \
	echo "Profiles written:"; \
	echo "  profiles/decoder.cpu.out"; \
	echo "  profiles/decoder.mem.out"; \
	echo ""; \
	echo "Flame graph URLs:"; \
	echo "  CPU:    http://localhost:$${cpu_port}/ui/flamegraph"; \
	echo "  Memory: http://localhost:$${mem_port}/ui/flamegraph"; \
	echo ""; \
	echo "Press Ctrl+C to stop both pprof servers."; \
	go tool pprof -http=:$${cpu_port} ./extractor.test profiles/decoder.cpu.out >/dev/null 2>&1 & \
	cpu_pid=$$!; \
	go tool pprof -http=:$${mem_port} ./extractor.test profiles/decoder.mem.out >/dev/null 2>&1 & \
	mem_pid=$$!; \
	trap 'kill $$cpu_pid $$mem_pid 2>/dev/null || true' INT TERM EXIT; \
	wait $$cpu_pid $$mem_pid
