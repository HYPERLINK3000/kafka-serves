package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/gobulgur/kafka-serves/pkg/kafka"
	"github.com/gobulgur/kafka-serves/services/gateway"
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

	// Создаём Kafka продюсер
	brokers := []string{"localhost:9092", "localhost:9093", "localhost:9094"}
	producer, err := kafka.NewProducer(brokers, db, logger)
	if err != nil {
		log.Fatalf("Failed to create producer: %v", err)
	}
	defer producer.Close()

	// Создаём gateway сервис
	gatewayService := gateway.NewService(producer, logger)

	// Настраиваем Gin router
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	// Prometheus metrics endpoint
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// API routes
	api := router.Group("/api/v1")
	{
		// Создание полиса
		api.POST("/policies", gatewayService.CreatePolicy)

		// Продление полиса
		api.POST("/policies/:id/renew", gatewayService.RenewPolicy)

		// Отмена полиса
		api.POST("/policies/:id/cancel", gatewayService.CancelPolicy)

		// Получение информации о полисе
		api.GET("/policies/:id", gatewayService.GetPolicy)

		// Список полисов клиента
		api.GET("/clients/:id/policies", gatewayService.GetClientPolicies)
	}

	// Настраиваем HTTP сервер
	srv := &http.Server{
		Addr:    ":8080",
		Handler: router,
	}

	// Запускаем сервер в горутине
	go func() {
		logger.Info("Starting Gateway service on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Ждём сигнал для graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down Gateway service...")

	// Graceful shutdown с таймаутом
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	logger.Info("Gateway service stopped")
}
