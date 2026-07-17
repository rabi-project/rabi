# SPDX-License-Identifier: Apache-2.0

.PHONY: build gen gen-check gen-python lint breaking test unit smoke \
	compose-up compose-down spdx-check bench bench-ci bench-publish

build:
	go build ./...

# Regenerate all code from the vendored spec. Committed output must match (CI-enforced).
gen:
	buf generate
	buf generate --template buf.gen.gateway.yaml --path spec/proto/tangle/api
	cp spec/schemas/quantumjob.schema.json internal/specdata/quantumjob.schema.json
	$(MAKE) gen-python

# Python stubs for the adapter protocol, generated into the adapter source
# root so `from tangle.adapter.v1alpha1 import ...` resolves naturally.
gen-python:
	cd adapters/aer && uv run --extra dev python -m grpc_tools.protoc \
		-I ../../spec/proto \
		--python_out=src --grpc_python_out=src --pyi_out=src \
		../../spec/proto/tangle/adapter/v1alpha1/adapter.proto
	touch adapters/aer/src/tangle/__init__.py \
		adapters/aer/src/tangle/adapter/__init__.py \
		adapters/aer/src/tangle/adapter/v1alpha1/__init__.py

gen-check: gen
	@git diff --exit-code -- gen/ internal/specdata/ || (echo "ERROR: generated code out of date; run 'make gen' and commit" && exit 1)

lint:
	buf lint
	golangci-lint run ./...

# Wire-contract stability: the vendored spec may never break against main.
breaking:
	buf breaking --against '.git#branch=main'

unit:
	go test ./...

test: unit

compose-up:
	docker compose -f deploy/compose/docker-compose.yml up -d --build --wait

compose-down:
	docker compose -f deploy/compose/docker-compose.yml down -v

# T0.smoke: empty stack boots and answers over real gRPC + REST.
smoke: compose-up
	./hack/smoke-m0.sh

spdx-check:
	./hack/check-spdx.sh

# ---- Artifact B: the benchmark (mvp-build-plan.md §M6) ----
# Full run: 5 seeds x 500 jobs x 3 policies over a 60h replay timeline.
# Deterministic: `make bench` twice => byte-identical CSVs (T6.det).
BENCH_OUT ?= out
BENCH_SEEDS ?= 5
BENCH_JOBS ?= 500
BENCH_HOURS ?= 60
BENCH_WORKERS ?= 4

bench:
	cd bench && uv run python scripts/gen_series.py --hours $(BENCH_HOURS) --out $(BENCH_OUT)/series.json
	go run ./bench/runner --seeds $(BENCH_SEEDS) --jobs $(BENCH_JOBS) \
		--series bench/$(BENCH_OUT)/series.json --out bench/$(BENCH_OUT)
	cd bench && uv run python scripts/execute.py --out $(BENCH_OUT) --workers $(BENCH_WORKERS)
	cd bench && uv run python scripts/analyze.py --out $(BENCH_OUT)
	@echo "benchmark report: bench/$(BENCH_OUT)/report.md"

# Reduced determinism gate for CI (T6.det): tiny run twice, CSVs must match.
bench-ci:
	./hack/bench-determinism.sh

# Copy the current run's publishable artifacts into the committed bench/results/.
bench-publish:
	mkdir -p bench/results
	cp bench/$(BENCH_OUT)/report.md bench/$(BENCH_OUT)/summary.csv \
	   bench/$(BENCH_OUT)/effects.csv bench/$(BENCH_OUT)/per_seed.csv \
	   bench/$(BENCH_OUT)/results.csv bench/$(BENCH_OUT)/*.png bench/results/
