.PHONY: run dev test vet build e2e mockup-shots docker deploy

run:
	go run ./cmd/server

dev:
	go run ./cmd/server -dev

test:
	go test ./...

vet:
	go vet ./...

build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server

e2e:
	cd e2e && npx playwright test

mockup-shots:
	cd e2e && npx playwright test mockups

docker:
	docker build -t whentorun .

deploy:
	fly deploy
