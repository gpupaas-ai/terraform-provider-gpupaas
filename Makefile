BINARY := terraform-provider-gpupaas
VERSION := 0.1.0
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

.PHONY: build test testacc fmt vet lint tidy install-local clean pin-sdk generate-docs docs-check brands check release

build:
	go build -o bin/$(BINARY) .

test:
	go test ./...

testacc:
	TF_ACC=1 TF_ACC_TERRAFORM_PATH=$${TF_ACC_TERRAFORM_PATH:-$$(command -v terraform)} go test ./... -v -count=1

fmt:
	go fmt ./...

vet:
	go vet ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

install-local: build
	mkdir -p $(HOME)/.terraform.d/plugins/gpupaas-ai/gpupaas/$(VERSION)/$(GOOS)_$(GOARCH)
	cp bin/$(BINARY) $(HOME)/.terraform.d/plugins/gpupaas-ai/gpupaas/$(VERSION)/$(GOOS)_$(GOARCH)/$(BINARY)_v$(VERSION)

clean:
	rm -rf bin/

# ---- release: one source, many brands, one pinned SDK line (see DEVELOPER_GUIDE.md section 9) ----

SDK_MODULE := github.com/gpupaas-ai/gpupaas-go
PLATFORMS ?= darwin/arm64,darwin/amd64,linux/amd64,linux/arm64,windows/amd64
TFPLUGINDOCS ?= tfplugindocs

# Strip the local replace and pin an immutable SDK tag: make pin-sdk SDK_VERSION=v0.4.0
pin-sdk:
	@test -n "$(SDK_VERSION)" || { echo "usage: make pin-sdk SDK_VERSION=vX.Y.Z"; exit 2; }
	go mod edit -dropreplace $(SDK_MODULE)
	go get $(SDK_MODULE)@$(SDK_VERSION)
	go mod tidy

# Regenerate the official provider's docs/ from the schema.
generate-docs:
	$(TFPLUGINDOCS) generate --provider-name gpupaas

# Fails when docs/ is stale against the schema.
docs-check: generate-docs
	git diff --exit-code docs/

# The brands of brands.yml.
brands:
	go run ./cmd/brandctl list

# Run the release gate without building: make check RELEASE_VERSION=0.4.0
# (RELEASE_VERSION has no default: a release names its version explicitly.)
check:
	@test -n "$(RELEASE_VERSION)" || { echo "usage: make check RELEASE_VERSION=X.Y.Z"; exit 2; }
	go run ./cmd/brandctl check -version $(RELEASE_VERSION)

# Gate, then build every brand from this commit into dist/<type>/ (docs, examples, zips, SHA256SUMS, manifest):
#   make release RELEASE_VERSION=0.4.0
release:
	@test -n "$(RELEASE_VERSION)" || { echo "usage: make release RELEASE_VERSION=X.Y.Z"; exit 2; }
	go run ./cmd/brandctl release -version $(RELEASE_VERSION) -platforms $(PLATFORMS) -tfplugindocs $(TFPLUGINDOCS)
