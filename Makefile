GIT_SHA := $(shell git rev-parse --short=7 HEAD 2>/dev/null)
VERSION ?= v0.0.0-local$(if $(GIT_SHA),+$(GIT_SHA))
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST := dist
TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64 windows/arm64
SCRIPT_ASSETS := install.sh install.ps1 qubership-dev-install.sh qubership-dev-install.ps1
BINARY_ASSETS := ai-agent-telemetry-darwin-amd64 ai-agent-telemetry-darwin-arm64 \
	ai-agent-telemetry-linux-amd64 ai-agent-telemetry-linux-arm64 \
	ai-agent-telemetry-windows-amd64.exe ai-agent-telemetry-windows-arm64.exe

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
	@sed 's|^BINARY_VERSION=.*|BINARY_VERSION="$(VERSION)"|' scripts/install.sh > $(DIST)/install.sh
	@sed "s|^\$$BinaryVersion = .*|\$$BinaryVersion = '$(VERSION)'|" scripts/install.ps1 > $(DIST)/install.ps1
	@test "$$(grep -c '^BINARY_VERSION="$(VERSION)"$$' $(DIST)/install.sh)" -eq 1
	@test "$$(grep -Fxc "\$$BinaryVersion = '$(VERSION)'" $(DIST)/install.ps1)" -eq 1
	@cp $(DIST)/install.sh $(DIST)/qubership-dev-install.sh
	@cp $(DIST)/install.ps1 $(DIST)/qubership-dev-install.ps1
	@cmp -s $(DIST)/install.sh $(DIST)/qubership-dev-install.sh
	@cmp -s $(DIST)/install.ps1 $(DIST)/qubership-dev-install.ps1
	@rm -f $(DIST)/bootstrap.sh $(DIST)/bootstrap.ps1

.PHONY: checksums
checksums: build
	@cd $(DIST) && shasum -a 256 $(BINARY_ASSETS) $(SCRIPT_ASSETS) > SHA256SUMS
	@test "$$(wc -l < $(DIST)/SHA256SUMS)" -eq 10
	@for asset in $(BINARY_ASSETS) $(SCRIPT_ASSETS); do \
		test "$$(awk -v name="$$asset" '$$2 == name { count++ } END { print count + 0 }' $(DIST)/SHA256SUMS)" -eq 1; \
	done
	@cat $(DIST)/SHA256SUMS

.PHONY: test
test:
	go test ./... -race

.PHONY: clean
clean:
	rm -rf $(DIST)
