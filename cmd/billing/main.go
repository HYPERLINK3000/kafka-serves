package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"

	"github.com/gobulgur/kafka-serves/pkg/kafka"
	"github.com/gobulgur/kafka-serves/services/billing"
)

func main() {
	// Настраиваем логгер
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	logger.SetFormatter(&logrus.JSONFormatter{})

	// Подключаемся к PostgreSQL
	db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=password dbname=insurance sslmode=disable")
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	// Конфигурация Kafka
	config := kafka.DefaultConfig()
	config.GroupID = "billing-service"
	config.Topic = "auto.events"
	config.DLQTopic = "auto.events.dlq"

	// Создаём handler для billing
	handler := billing.NewHandler(db, logger)

	// Создаём consumer
	consumer, err := kafka.NewConsumer(config, handler, db, logger)
	if err != nil {
		log.Fatalf("Failed to create consumer: %v", err)
	}

	// Контекст для graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Обработка сигналов для graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info("Received shutdown signal, stopping consumer...")
		cancel()
	}()

	logger.Info("Starting Billing consumer service...")

	// Запускаем consumer
	if err := consumer.Start(ctx); err != nil {
		log.Fatalf("Consumer error: %v", err)
	}

	logger.Info("Billing consumer service stopped")
}
