build:
	cd src/ && go mod tidy && GOOS=linux GOARCH=amd64 go build -o bin/seeone .

run: build
	docker-compose up --build --remove-orphans

down:
	docker-compose down