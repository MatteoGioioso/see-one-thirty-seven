build:
	cd src/ && go mod tidy && GOOS=linux GOARCH=amd64 go build -o bin/seeone .
	cd pgbench/ && go mod tidy && GOOS=linux GOARCH=amd64 go build -o bin/bench .

run: build
	NAME_1=etcd-1 NAME_2=etcd-2 NAME_3=etcd-3 HOST_1=etcd1 HOST_2=etcd2 HOST_3=etcd3 docker-compose up --build --remove-orphans

down:
	docker-compose down