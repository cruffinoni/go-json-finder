SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

.DEFAULT_GOAL := help

GO_TEST ?= go test
BENCH_COUNT ?= 7
BENCH_OUTPUT ?=
BENCH_TRACE ?=0

BENCH_ARGS := ./extractor -run=^$$ -bench=BenchmarkExtract_ -benchmem -count=$(BENCH_COUNT)
BENCH_ARGS_DECODER_ONLY := ./extractor -run=^$$ -bench='BenchmarkExtract_.*/decoder' -benchmem -count=$(BENCH_COUNT)
BENCH_LARGE_ARGS_DECODER_ONLY := ./extractor -run=^$$ -bench='BenchmarkExtract_[A-Za-z]+_Large.*/decoder' -benchmem -count=$(BENCH_COUNT)
PROFILE_BENCH := BenchmarkExtract_[A-Za-z]+_Large.*/decoder

BASELINE_FILE_NAME := baseline.txt
BENCH_HISTORY_FOLDER := bench-history
BASELINE_HISTORY_PATH := $(BENCH_HISTORY_FOLDER)/$(BASELINE_FILE_NAME)

.PHONY: help test bench profile compare compare-large compare-update-baseline

define run_compare
@tmp_file="$$(mktemp)"; \
trap 'rm -f "$$tmp_file"' EXIT; \
if [ ! -s "$(BASELINE_HISTORY_PATH)" ]; then \
	echo "Baseline file missing or empty: $(BASELINE_HISTORY_PATH)"; \
	echo "Run: make compare-update-baseline"; \
	exit 1; \
fi; \
if ! grep -Eq '^BenchmarkExtract_' "$(BASELINE_HISTORY_PATH)"; then \
	echo "Baseline file has no benchmark data: $(BASELINE_HISTORY_PATH)"; \
	echo "Run: make compare-update-baseline"; \
	exit 1; \
fi; \
if ! command -v benchstat >/dev/null 2>&1; then \
	echo "benchstat not found in PATH."; \
	echo "Install: go install golang.org/x/perf/cmd/benchstat@latest"; \
	exit 1; \
fi; \
$(GO_TEST) $(1) | tee "$$tmp_file"; \
if ! grep -Eq '^BenchmarkExtract_' "$$tmp_file"; then \
	echo "Current benchmark run produced no benchmark data."; \
	echo "Check benchmark filters or run without filters."; \
	exit 1; \
fi; \
benchstat "$(BASELINE_HISTORY_PATH)" "$$tmp_file"
endef

help:
	@echo "Targets:"
	@echo "  make test          Run all unit tests"
	@echo "  make bench         Run benchmarks and print a compact cross-extractor summary"
	@echo "                     Options: BENCH_OUTPUT=<path> (save output), BENCH_TRACE=1 (enable shell trace)"
	@echo "  make compare       Compare decoder-only benchmark results with baseline"
	@echo "  make compare-large Compare decoder-only large-case benchmark results with baseline"
	@echo "  make compare-update-baseline  Refresh baseline benchmark file (backs up previous baseline)"
	@echo "  make profile       Collect CPU+MEM profiles and open interactive CPU pprof"

test:
	$(GO_TEST) ./...

bench:
	@tmp_file="$$(mktemp)"; \
	trap 'rm -f "$$tmp_file"' EXIT; \
	bench_output="$(BENCH_OUTPUT)"; \
	if [ -n "$$bench_output" ]; then \
		mkdir -p "$$(dirname "$$bench_output")"; \
		: > "$$bench_output"; \
	fi; \
	if [ "$(BENCH_TRACE)" = "1" ]; then set -x; fi; \
	if [ -n "$$bench_output" ]; then \
		$(GO_TEST) $(BENCH_ARGS) | tee "$$tmp_file" | tee -a "$$bench_output"; \
	else \
		$(GO_TEST) $(BENCH_ARGS) | tee "$$tmp_file"; \
	fi; \
	use_color=1; \
	if [ -n "$$bench_output" ] || [ -n "$$NO_COLOR" ] || [ ! -t 1 ]; then use_color=0; fi; \
	if [ -n "$$bench_output" ]; then \
		{ \
			echo ""; \
			echo "=== Summary by metric (decoder | struct | gjson | fastjson | sonic | go-json | easyjson, lower is better) ==="; \
			awk -v USE_COLOR="$$use_color" -f scripts/bench_table.awk "$$tmp_file"; \
		} | tee -a "$$bench_output"; \
	else \
		echo ""; \
		echo "=== Summary by metric (decoder | struct | gjson | fastjson | sonic | go-json | easyjson, lower is better) ==="; \
		awk -v USE_COLOR="$$use_color" -f scripts/bench_table.awk "$$tmp_file"; \
	fi

compare-update-baseline:
	@mkdir -p $(BENCH_HISTORY_FOLDER)
	@tmp_file="$$(mktemp)"; \
	trap 'rm -f "$$tmp_file"' EXIT; \
	if [ -f "$(BASELINE_HISTORY_PATH)" ]; then \
		ts="$$(date +%Y%m%d-%H%M%S)"; \
		backup_path="$(BASELINE_HISTORY_PATH).$$ts.$$$$.bak"; \
		cp "$(BASELINE_HISTORY_PATH)" "$$backup_path"; \
		echo "Backed up existing baseline to $$backup_path"; \
	fi; \
	$(GO_TEST) $(BENCH_ARGS_DECODER_ONLY) | tee "$$tmp_file"; \
	if ! grep -Eq '^BenchmarkExtract_' "$$tmp_file"; then \
		echo "No benchmark lines captured. Baseline was not updated."; \
		exit 1; \
	fi; \
	mv "$$tmp_file" "$(BASELINE_HISTORY_PATH)"; \
	trap - EXIT; \
	echo "Updated baseline at $(BASELINE_HISTORY_PATH)"

compare-large:
	$(call run_compare,$(BENCH_LARGE_ARGS_DECODER_ONLY))

compare:
	$(call run_compare,$(BENCH_ARGS_DECODER_ONLY))

profile:
	@mkdir -p profiles
	$(GO_TEST) ./extractor -run='^$$' -bench='$(PROFILE_BENCH)' -benchmem -cpuprofile=profiles/decoder.cpu.out -memprofile=profiles/decoder.mem.out
	@echo ""
	@echo "Profiles written:"
	@echo "  profiles/decoder.cpu.out"
	@echo "  profiles/decoder.mem.out"
	@echo ""
	@echo "Launching interactive CPU pprof..."
	@echo "Tip: run 'go tool pprof ./extractor.test profiles/decoder.mem.out' for memory profile."
	go tool pprof ./extractor.test profiles/decoder.cpu.out
