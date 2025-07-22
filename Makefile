.PHONY: help build test clean docker-up docker-down kafka-topics run-gateway run-underwriting run-billing

# Переменные
DOCKER_COMPOSE = docker-compose
GO_VERSION = 1.22
PROJECT_NAME = kafka-serves

help: ## Показать справку
	@echo "Доступные команды:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Собрать все сервисы
	@echo "Сборка сервисов..."
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/underwriting ./cmd/underwriting  
	go build -o bin/billing ./cmd/billing
	@echo "✅ Сборка завершена"



clean: ## Очистить собранные файлы
	@echo "Очистка..."
	rm -rf bin/
	go clean
	@echo "✅ Очистка завершена"

docker-up: ## Запустить инфраструктуру (Kafka, PostgreSQL, мониторинг)
	@echo "Запуск инфраструктуры..."
	$(DOCKER_COMPOSE) up -d
	@echo "✅ Инфраструктура запущена"
	@echo "🔗 Полезные ссылки:"
	@echo "   Kafka UI:    http://localhost:8080"
	@echo "   Grafana:     http://localhost:3000 (admin/admin)"
	@echo "   Prometheus:  http://localhost:9090"

docker-down: ## Остановить инфраструктуру
	@echo "Остановка инфраструктуры..."
	$(DOCKER_COMPOSE) down -v
	@echo "✅ Инфраструктура остановлена"

kafka-topics: ## Создать необходимые Kafka топики
	@echo "Создание Kafka топиков..."
	docker exec kafka1 kafka-topics --create --topic auto.events --partitions 3 --replication-factor 3 --bootstrap-server localhost:29092 || true
	docker exec kafka1 kafka-topics --create --topic auto.events.dlq --partitions 3 --replication-factor 3 --bootstrap-server localhost:29092 || true
	@echo "✅ Топики созданы"

run-gateway: build ## Запустить Gateway сервис
	@echo "Запуск Gateway сервиса на порту 8080..."
	./bin/gateway

run-underwriting: build ## Запустить Underwriting сервис
	@echo "Запуск Underwriting сервиса..."
	./bin/underwriting

run-billing: build ## Запустить Billing сервис
	@echo "Запуск Billing сервиса..."
	./bin/billing

run-all: build ## Запустить все сервисы параллельно
	@echo "Запуск всех сервисов..."
	./bin/gateway & \
	./bin/underwriting & \
	./bin/billing & \
	wait

demo: docker-up kafka-topics build ## Запустить полную демонстрацию системы
	@echo "🚀 Запуск демонстрации системы..."
	@echo "Ожидание готовности инфраструктуры..."
	sleep 20
	@echo "Запуск сервисов..."
	./bin/gateway &
	sleep 2
	./bin/underwriting &
	sleep 2
	./bin/billing &
	@echo "✅ Все сервисы запущены!"
	@echo ""
	@echo "🔗 Полезные ссылки:"
	@echo "   Gateway API: http://localhost:8080"
	@echo "   Kafka UI:    http://localhost:8080"
	@echo "   Grafana:     http://localhost:3000"
	@echo "   Prometheus:  http://localhost:9090"
	@echo ""
	@echo "📝 Тестовые команды:"
	@echo "   curl -X POST http://localhost:8080/api/v1/policies \\"
	@echo "        -H 'Content-Type: application/json' \\"
	@echo "        -d '{\"client_id\":\"test-123\",\"policy_type\":\"auto\",\"driver_age\":30,\"driving_experience\":10,\"car_type\":\"sedan\",\"region\":\"moscow\",\"accidents_count\":0}'"
	@echo ""
	@echo "Нажмите Ctrl+C для остановки"
	wait

stop-demo: ## Остановить демонстрацию
	@echo "Остановка демонстрации..."
	pkill -f "bin/gateway" || true
	pkill -f "bin/underwriting" || true  
	pkill -f "bin/billing" || true
	$(MAKE) docker-down
	@echo "✅ Демонстрация остановлена"

deps: ## Установить зависимости
	@echo "Установка зависимостей..."
	go mod download
	go mod tidy
	@echo "✅ Зависимости установлены"

format: ## Форматировать код
	@echo "Форматирование кода..."
	go fmt ./...
	@echo "✅ Код отформатирован"

# Команды для разработки
dev-setup: deps docker-up kafka-topics ## Настройка среды разработки
	@echo "✅ Среда разработки готова"

dev-reset: docker-down clean dev-setup ## Сброс среды разработки

# Информационные команды
status: ## Показать статус сервисов
	@echo "Статус Docker контейнеров:"
	$(DOCKER_COMPOSE) ps
	@echo ""
	@echo "Статус Go процессов:"
	@pgrep -f "bin/" | wc -l | xargs echo "Запущено Go сервисов:"

logs: ## Показать логи инфраструктуры
	$(DOCKER_COMPOSE) logs -f

version: ## Показать версию Go
	@echo "Go version: $(shell go version)"
	@echo "Project: $(PROJECT_NAME)" 