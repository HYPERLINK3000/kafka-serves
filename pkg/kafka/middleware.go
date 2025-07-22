package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/Shopify/sarama"
	"github.com/sirupsen/logrus"
)

// LoggingMiddleware логирует обработку сообщений
type LoggingMiddleware struct {
	logger *logrus.Logger
}

// NewLoggingMiddleware создаёт новый LoggingMiddleware
func NewLoggingMiddleware(logger *logrus.Logger) *LoggingMiddleware {
	return &LoggingMiddleware{logger: logger}
}

// Process обрабатывает сообщение с логированием
func (m *LoggingMiddleware) Process(ctx context.Context, message *sarama.ConsumerMessage, next func(context.Context, *sarama.ConsumerMessage) error) error {
	start := time.Now()

	m.logger.WithFields(logrus.Fields{
		"topic":     message.Topic,
		"partition": message.Partition,
		"offset":    message.Offset,
		"key":       string(message.Key),
	}).Debug("Processing message")

	err := next(ctx, message)

	duration := time.Since(start)
	fields := logrus.Fields{
		"topic":     message.Topic,
		"partition": message.Partition,
		"offset":    message.Offset,
		"duration":  duration,
	}

	if err != nil {
		m.logger.WithError(err).WithFields(fields).Error("Message processing failed")
	} else {
		m.logger.WithFields(fields).Info("Message processed successfully")
	}

	return err
}

// MetricsMiddleware собирает метрики обработки
type MetricsMiddleware struct {
	metrics *ConsumerMetrics
}

// NewMetricsMiddleware создаёт новый MetricsMiddleware
func NewMetricsMiddleware(metrics *ConsumerMetrics) *MetricsMiddleware {
	return &MetricsMiddleware{metrics: metrics}
}

// Process обрабатывает сообщение с сбором метрик
func (m *MetricsMiddleware) Process(ctx context.Context, message *sarama.ConsumerMessage, next func(context.Context, *sarama.ConsumerMessage) error) error {
	start := time.Now()

	err := next(ctx, message)

	duration := time.Since(start)
	m.metrics.ProcessingTime.Observe(duration.Seconds())

	if err != nil {
		m.metrics.Errors.Inc()
	} else {
		m.metrics.MessagesProcessed.Inc()
	}

	return err
}

// RetryMiddleware реализует логику повторов
type RetryMiddleware struct {
	maxRetries int
	retryDelay time.Duration
	logger     *logrus.Logger
}

// NewRetryMiddleware создаёт новый RetryMiddleware
func NewRetryMiddleware(maxRetries int, retryDelay time.Duration, logger *logrus.Logger) *RetryMiddleware {
	return &RetryMiddleware{
		maxRetries: maxRetries,
		retryDelay: retryDelay,
		logger:     logger,
	}
}

// Process обрабатывает сообщение с логикой повторов
func (m *RetryMiddleware) Process(ctx context.Context, message *sarama.ConsumerMessage, next func(context.Context, *sarama.ConsumerMessage) error) error {
	var lastErr error

	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		if attempt > 0 {
			m.logger.WithFields(logrus.Fields{
				"attempt":   attempt,
				"topic":     message.Topic,
				"partition": message.Partition,
				"offset":    message.Offset,
			}).Warn("Retrying message processing")

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(m.retryDelay):
				// Продолжаем после задержки
			}
		}

		err := next(ctx, message)
		if err == nil {
			if attempt > 0 {
				m.logger.WithFields(logrus.Fields{
					"attempt":   attempt,
					"topic":     message.Topic,
					"partition": message.Partition,
					"offset":    message.Offset,
				}).Info("Message processing succeeded after retry")
			}
			return nil
		}

		lastErr = err

		// Проверяем, стоит ли повторять попытку
		if !isRetryableError(err) {
			m.logger.WithError(err).WithFields(logrus.Fields{
				"topic":     message.Topic,
				"partition": message.Partition,
				"offset":    message.Offset,
			}).Error("Non-retryable error, giving up")
			break
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", m.maxRetries+1, lastErr)
}

// isRetryableError определяет, можно ли повторить попытку при данной ошибке
func isRetryableError(err error) bool {
	// Здесь можно добавить логику определения повторяемых ошибок
	// Например, временные сетевые ошибки, ошибки базы данных и т.д.

	// Пока что считаем все ошибки повторяемыми, кроме контекстных
	if err == context.Canceled || err == context.DeadlineExceeded {
		return false
	}

	return true
}
