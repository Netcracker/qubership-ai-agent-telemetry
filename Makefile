VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST := dist
TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64

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

.PHONY: checksums
checksums: build
	@cd $(DIST) && shasum -a 256 ai-agent-telemetry-* > SHA256SUMS && cat SHA256SUMS

.PHONY: test
test:
	go test ./... -race

.PHONY: clean
clean:
	rm -rf $(DIST)
