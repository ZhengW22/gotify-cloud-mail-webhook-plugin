.PHONY: test vet build-arm64 build-arm7 build-amd64

test:
	go test -race ./...

vet:
	go vet ./...

build-arm64:
	bash scripts/build.sh arm64

build-arm7:
	bash scripts/build.sh arm-7

build-amd64:
	bash scripts/build.sh amd64
