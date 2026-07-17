# SPDX-License-Identifier: Apache-2.0

.PHONY: build gen gen-check lint test unit smoke compose-up compose-down spdx-check

build:
	go build ./...

# Regenerate all code from the vendored spec. Committed output must match (CI-enforced).
gen:
	buf generate
	buf generate --template buf.gen.gateway.yaml --path spec/proto/tangle/api
	cp spec/schemas/quantumjob.schema.json internal/specdata/quantumjob.schema.json

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
