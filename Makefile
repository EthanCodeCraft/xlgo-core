.PHONY: build run test clean tidy docker

# 构建二进制文件
build:
	go build -o bin/server ./example

# 开发模式运行
run:
	go run ./example

# 运行测试
test:
	go test ./...

# 清理
clean:
	rm -rf bin/
	rm -rf logs/

# 安装依赖
tidy:
	go mod tidy

# 生成 Swagger 文档
swagger:
	swag init -g example/main.go -o example/swagger

# Docker 构建
docker:
	docker build -t xlgo-app:latest .

# Docker 运行
docker-run:
	docker run -d -p 8080:8080 xlgo-app:latest
