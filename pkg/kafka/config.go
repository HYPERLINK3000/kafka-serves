package kafka

import (
	"time"

	"github.com/Shopify/sarama"
)

// Config содержит настройки для Kafka клиентов
type Config struct {
	Brokers           []string      `yaml:"brokers"`
	GroupID           string        `yaml:"group_id"`
	Topic             string        `yaml:"topic"`
	RetryAttempts     int           `yaml:"retry_attempts"`
	RetryDelay        time.Duration `yaml:"retry_delay"`
	ProcessingTimeout time.Duration `yaml:"processing_timeout"`
	DLQTopic          string        `yaml:"dlq_topic"`
}

// DefaultConfig возвращает конфигурацию по умолчанию
func DefaultConfig() *Config {
	return &Config{
		Brokers:           []string{"localhost:9092", "localhost:9093", "localhost:9094"},
		RetryAttempts:     3,
		RetryDelay:        time.Second * 2,
		ProcessingTimeout: time.Second * 30,
	}
}

// NewProducerConfig создаёт конфигурацию для продюсера с exactly-once семантикой
func NewProducerConfig() *sarama.Config {
	config := sarama.NewConfig()

	// Exactly-once настройки
	config.Producer.Idempotent = true                // Идемпотентный продюсер
	config.Producer.RequiredAcks = sarama.WaitForAll // Ждём подтверждения от всех реплик
	config.Producer.Retry.Max = 5                    // Максимум повторов
	config.Producer.Return.Successes = true          // Возвращаем успешные отправки
	config.Producer.Return.Errors = true             // Возвращаем ошибки
	config.Producer.Flush.Messages = 1               // Отправляем сразу

	// Партиционирование
	config.Producer.Partitioner = sarama.NewHashPartitioner

	// Настройки для транзакций
	config.Producer.Transaction.ID = "insurance-producer"
	config.Producer.Transaction.Timeout = time.Second * 30

	// Компрессия для производительности
	config.Producer.Compression = sarama.CompressionSnappy

	// Версия протокола
	config.Version = sarama.V2_8_0_0

	return config
}

// NewConsumerConfig создаёт конфигурацию для консьюмера
func NewConsumerConfig(groupID string) *sarama.Config {
	config := sarama.NewConfig()

	// Exactly-once настройки для консьюмера
	config.Consumer.Offsets.Initial = sarama.OffsetNewest
	config.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	config.Consumer.Group.Rebalance.Timeout = time.Second * 60
	config.Consumer.Group.Session.Timeout = time.Second * 10
	config.Consumer.Group.Heartbeat.Interval = time.Second * 3

	// Отключаем автокоммит - будем коммитить вручную после обработки
	config.Consumer.Offsets.AutoCommit.Enable = false

	// Версия протокола
	config.Version = sarama.V2_8_0_0

	return config
}
