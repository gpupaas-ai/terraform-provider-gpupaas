BINARY := terraform-provider-gpupaas
VERSION := 0.1.0
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

.PHONY: build test testacc fmt vet lint tidy install-local clean

build:
	go build -o bin/$(BINARY) .

test:
	go test ./...

testacc:
	TF_ACC=1 go test ./... -v -count=1

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
