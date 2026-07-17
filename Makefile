GIT_SHA := $(shell git rev-parse --short=7 HEAD 2>/dev/null)
VERSION ?= v0.0.0-local$(if $(GIT_SHA),+$(GIT_SHA))
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST := dist
TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64 windows/arm64

.PHONY: build
build:
	@mkdir -p $(DIST)
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=$(DIST)/ai-agent-telemetry-$$os-$$arch$$ext; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $$out . ; \
	done
	@cp scripts/install.sh $(DIST)/install.sh
	@cp scripts/install.ps1 $(DIST)/install.ps1
	@cp scripts/install.sh $(DIST)/bootstrap.sh
	@cp scripts/install.ps1 $(DIST)/bootstrap.ps1
	@cp global-scripts/qubership-dev-install.sh $(DIST)/qubership-dev-install.sh
	@cp global-scripts/qubership-dev-install.ps1 $(DIST)/qubership-dev-install.ps1

.PHONY: checksums
checksums: build
	@cd $(DIST) && shasum -a 256 ai-agent-telemetry-* install.sh install.ps1 bootstrap.sh bootstrap.ps1 \
		qubership-dev-install.sh qubership-dev-install.ps1 > SHA256SUMS && cat SHA256SUMS

.PHONY: test
test:
	go test ./... -race

.PHONY: clean
clean:
	rm -rf $(DIST)
