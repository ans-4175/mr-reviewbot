VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/ans-4175/mr-reviewbot/internal/app.Version=$(VERSION)
PLATFORMS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

.PHONY: build test vet dist release clean
build:
	go build -ldflags "$(LDFLAGS)" -o bin/reviewbot ./cmd/reviewbot

vet:
	go vet ./...

test: vet
	go test ./...

# Release archives: dist/mr-reviewbot_<os>_<arch>.tar.gz + checksums.txt (what scripts/install.sh downloads).
dist: test
	rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; n=mr-reviewbot_$${os}_$${arch}; \
		mkdir -p dist/$$n/skills && \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$$n/reviewbot ./cmd/reviewbot && \
		cp README.md SETUP.md LICENSE NOTICE config.example.json dist/$$n/ && \
		cp -R skills/review-mr dist/$$n/skills/ && \
		tar -C dist -czf dist/$$n.tar.gz $$n && rm -rf dist/$$n && \
		echo "built dist/$$n.tar.gz"; \
	done
	cd dist && shasum -a 256 *.tar.gz > checksums.txt

# Needs a tagged, clean commit and a GitHub remote: git tag v0.1.0 && make release
release: dist
	@case "$(VERSION)" in v*-*|*dirty*|dev) echo "release needs a clean tag like v0.1.0, got $(VERSION)"; exit 1;; esac
	gh release create $(VERSION) dist/*.tar.gz dist/checksums.txt --title $(VERSION) --generate-notes

clean:
	rm -rf bin dist
