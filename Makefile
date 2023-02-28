build:
	cd src/ && go mod tidy && GOOS=linux GOARCH=amd64 go build -o bin/seeone .
	cd pgbench/ && go mod tidy && GOOS=linux GOARCH=amd64 go build -o bin/bench .

run: build
	NAME_1=etcd-1 NAME_2=etcd-2 NAME_3=etcd-3 HOST_1=etcd1 HOST_2=etcd2 HOST_3=etcd3 docker-compose up --build --remove-orphans

down:
	docker-compose down

shutdown.node1:
	curl http://localhost:8085/shutdown

shutdown.node2:
	curl http://localhost:8086/shutdown

shutdown.node3:
	curl http://localhost:8087/shutdown

start.node1:
	NAME_1=etcd-1 NAME_2=etcd-2 NAME_3=etcd-3 HOST_1=etcd1 HOST_2=etcd2 HOST_3=etcd3 docker-compose up --build --remove-orphans postgresql1 -d

start.node2:
	NAME_1=etcd-1 NAME_2=etcd-2 NAME_3=etcd-3 HOST_1=etcd1 HOST_2=etcd2 HOST_3=etcd3 docker-compose up --build --remove-orphans postgresql2 -d

start.node3:
	NAME_1=etcd-1 NAME_2=etcd-2 NAME_3=etcd-3 HOST_1=etcd1 HOST_2=etcd2 HOST_3=etcd3 docker-compose up --build --remove-orphans postgresql3 -d
