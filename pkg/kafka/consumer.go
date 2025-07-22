package kafka

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Shopify/sarama"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
)

// MessageHandler определяет интерфейс для обработки сообщений
type MessageHandler interface {
	Handle(ctx context.Context, message *sarama.ConsumerMessage) error
	GetTopic() string
}

// Middleware определяет интерфейс для промежуточного ПО
type Middleware interface {
	Process(ctx context.Context, message *sarama.ConsumerMessage, next func(context.Context, *sarama.ConsumerMessage) error) error
}

// Consumer представляет Kafka консьюмер с расширенными возможностями
type Consumer struct {
	config      *Config
	handler     MessageHandler
	middlewares []Middleware
	logger      *logrus.Logger
	db          *sql.DB
	producer    sarama.SyncProducer
	metrics     *ConsumerMetrics
}

// ConsumerMetrics содержит метрики для мониторинга
type ConsumerMetrics struct {
	MessagesProcessed prometheus.Counter
	ProcessingTime    prometheus.Histogram
	Errors            prometheus.Counter
	Retries           prometheus.Counter
	DLQMessages       prometheus.Counter
	Lag               prometheus.Gauge
}

// NewConsumer создаёт новый консьюмер
func NewConsumer(config *Config, handler MessageHandler, db *sql.DB, logger *logrus.Logger) (*Consumer, error) {
	// Создаём продюсер для DLQ
	producerConfig := NewProducerConfig()
	producer, err := sarama.NewSyncProducer(config.Brokers, producerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create producer: %w", err)
	}

	// Инициализируем метрики
	metrics := &ConsumerMetrics{
		MessagesProcessed: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kafka_messages_processed_total",
			Help: "Total number of processed messages",
			ConstLabels: prometheus.Labels{
				"topic": handler.GetTopic(),
			},
		}),
		ProcessingTime: promauto.NewHistogram(prometheus.HistogramOpts{
			Name: "kafka_message_processing_duration_seconds",
			Help: "Time spent processing messages",
			ConstLabels: prometheus.Labels{
				"topic": handler.GetTopic(),
			},
		}),
		Errors: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kafka_processing_errors_total",
			Help: "Total number of processing errors",
			ConstLabels: prometheus.Labels{
				"topic": handler.GetTopic(),
			},
		}),
		Retries: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kafka_retries_total",
			Help: "Total number of retries",
			ConstLabels: prometheus.Labels{
				"topic": handler.GetTopic(),
			},
		}),
		DLQMessages: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kafka_dlq_messages_total",
			Help: "Total number of messages sent to DLQ",
			ConstLabels: prometheus.Labels{
				"topic": handler.GetTopic(),
			},
		}),
		Lag: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "kafka_consumer_lag",
			Help: "Consumer lag",
			ConstLabels: prometheus.Labels{
				"topic": handler.GetTopic(),
			},
		}),
	}

	consumer := &Consumer{
		config:   config,
		handler:  handler,
		logger:   logger,
		db:       db,
		producer: producer,
		metrics:  metrics,
	}

	// Добавляем стандартные middleware
	consumer.Use(NewLoggingMiddleware(logger))
	consumer.Use(NewMetricsMiddleware(metrics))
	consumer.Use(NewRetryMiddleware(config.RetryAttempts, config.RetryDelay, logger))

	return consumer, nil
}

// Use добавляет middleware
func (c *Consumer) Use(middleware Middleware) {
	c.middlewares = append(c.middlewares, middleware)
}

// Start запускает консьюмер
func (c *Consumer) Start(ctx context.Context) error {
	consumerConfig := NewConsumerConfig(c.config.GroupID)
	consumerGroup, err := sarama.NewConsumerGroup(c.config.Brokers, c.config.GroupID, consumerConfig)
	if err != nil {
		return fmt.Errorf("failed to create consumer group: %w", err)
	}
	defer consumerGroup.Close()

	// Запускаем обработку сообщений
	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Consumer context cancelled")
			return nil
		default:
			err := consumerGroup.Consume(ctx, []string{c.config.Topic}, c)
			if err != nil {
				c.logger.WithError(err).Error("Error from consumer")
				return err
			}
		}
	}
}

// Setup реализует интерфейс sarama.ConsumerGroupHandler
func (c *Consumer) Setup(sarama.ConsumerGroupSession) error {
	c.logger.Info("Consumer group session started")
	return nil
}

// Cleanup реализует интерфейс sarama.ConsumerGroupHandler
func (c *Consumer) Cleanup(sarama.ConsumerGroupSession) error {
	c.logger.Info("Consumer group session ended")
	return nil
}

// ConsumeClaim реализует интерфейс sarama.ConsumerGroupHandler
func (c *Consumer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case message := <-claim.Messages():
			if message == nil {
				return nil
			}

			// Обрабатываем сообщение через middleware chain
			err := c.processMessage(session.Context(), message)
			if err != nil {
				c.logger.WithError(err).WithFields(logrus.Fields{
					"topic":     message.Topic,
					"partition": message.Partition,
					"offset":    message.Offset,
				}).Error("Failed to process message")

				// Отправляем в DLQ если все попытки исчерпаны
				if c.config.DLQTopic != "" {
					c.sendToDLQ(message, err)
				}
			}

			// Коммитим offset только после успешной обработки
			session.MarkOffset(message.Topic, message.Partition, message.Offset+1, "")

		case <-session.Context().Done():
			return nil
		}
	}
}

// processMessage обрабатывает сообщение через цепочку middleware
func (c *Consumer) processMessage(ctx context.Context, message *sarama.ConsumerMessage) error {
	// Создаём цепочку middleware
	var next func(context.Context, *sarama.ConsumerMessage) error
	next = c.handler.Handle

	// Применяем middleware в обратном порядке
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		middleware := c.middlewares[i]
		currentNext := next
		next = func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			return middleware.Process(ctx, msg, currentNext)
		}
	}

	return next(ctx, message)
}

// sendToDLQ отправляет сообщение в Dead Letter Queue
func (c *Consumer) sendToDLQ(originalMessage *sarama.ConsumerMessage, processingError error) {
	dlqMessage := &DLQMessage{
		OriginalTopic:     originalMessage.Topic,
		OriginalPartition: originalMessage.Partition,
		OriginalOffset:    originalMessage.Offset,
		OriginalKey:       string(originalMessage.Key),
		OriginalValue:     string(originalMessage.Value),
		Error:             processingError.Error(),
		Timestamp:         time.Now(),
	}

	dlqBytes, err := json.Marshal(dlqMessage)
	if err != nil {
		c.logger.WithError(err).Error("Failed to marshal DLQ message")
		return
	}

	_, _, err = c.producer.SendMessage(&sarama.ProducerMessage{
		Topic: c.config.DLQTopic,
		Key:   sarama.ByteEncoder(originalMessage.Key),
		Value: sarama.StringEncoder(dlqBytes),
	})

	if err != nil {
		c.logger.WithError(err).Error("Failed to send message to DLQ")
	} else {
		c.metrics.DLQMessages.Inc()
		c.logger.WithFields(logrus.Fields{
			"dlq_topic":      c.config.DLQTopic,
			"original_topic": originalMessage.Topic,
			"offset":         originalMessage.Offset,
		}).Warn("Message sent to DLQ")
	}
}

// DLQMessage представляет сообщение в Dead Letter Queue
type DLQMessage struct {
	OriginalTopic     string    `json:"original_topic"`
	OriginalPartition int32     `json:"original_partition"`
	OriginalOffset    int64     `json:"original_offset"`
	OriginalKey       string    `json:"original_key"`
	OriginalValue     string    `json:"original_value"`
	Error             string    `json:"error"`
	Timestamp         time.Time `json:"timestamp"`
}
