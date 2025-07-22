package underwriting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/Shopify/sarama"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/gobulgur/kafka-serves/pkg/kafka"
)

// Handler обрабатывает события для расчёта страховых премий
type Handler struct {
	db     *sql.DB
	logger *logrus.Logger
}

// NewHandler создаёт новый handler для underwriting
func NewHandler(db *sql.DB, logger *logrus.Logger) *Handler {
	return &Handler{
		db:     db,
		logger: logger,
	}
}

// Handle реализует интерфейс kafka.MessageHandler
func (h *Handler) Handle(ctx context.Context, message *sarama.ConsumerMessage) error {
	// Парсим событие
	var event kafka.PolicyEvent
	if err := json.Unmarshal(message.Value, &event); err != nil {
		return fmt.Errorf("failed to unmarshal event: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"event_id":   event.ID,
		"policy_id":  event.PolicyID,
		"event_type": event.EventType,
	}).Info("Processing underwriting event")

	// Обрабатываем в зависимости от типа события
	switch event.EventType {
	case "created":
		return h.handlePolicyCreated(ctx, &event)
	case "renewed":
		return h.handlePolicyRenewed(ctx, &event)
	case "cancelled":
		return h.handlePolicyCancelled(ctx, &event)
	default:
		h.logger.WithField("event_type", event.EventType).Warn("Unknown event type, skipping")
		return nil
	}
}

// GetTopic возвращает топик, который обрабатывает этот handler
func (h *Handler) GetTopic() string {
	return "auto.events"
}

// handlePolicyCreated обрабатывает создание нового полиса
func (h *Handler) handlePolicyCreated(ctx context.Context, event *kafka.PolicyEvent) error {
	// Извлекаем данные полиса из события
	policyData, ok := event.EventData["policy"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid policy data in event")
	}

	// Рассчитываем премию на основе факторов риска
	calculation, err := h.calculatePremium(policyData)
	if err != nil {
		return fmt.Errorf("failed to calculate premium: %w", err)
	}

	// Сохраняем расчёт в базу данных
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO insurance.premium_calculations 
		(id, policy_id, base_premium, risk_factors, final_premium, calculated_at, calculation_version) 
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.New().String(),
		event.PolicyID,
		calculation.BasePremium,
		calculation.RiskFactorsJSON,
		calculation.FinalPremium,
		time.Now(),
		1,
	)

	if err != nil {
		return fmt.Errorf("failed to save premium calculation: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"policy_id":     event.PolicyID,
		"base_premium":  calculation.BasePremium,
		"final_premium": calculation.FinalPremium,
		"risk_score":    calculation.RiskScore,
	}).Info("Premium calculated successfully")

	return nil
}

// handlePolicyRenewed обрабатывает продление полиса
func (h *Handler) handlePolicyRenewed(ctx context.Context, event *kafka.PolicyEvent) error {
	// При продлении пересчитываем премию с учётом новых данных
	policyData, ok := event.EventData["policy"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid policy data in event")
	}

	// Получаем предыдущую версию расчёта
	var previousVersion int
	err := h.db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(calculation_version), 0) FROM insurance.premium_calculations WHERE policy_id = $1",
		event.PolicyID,
	).Scan(&previousVersion)

	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to get previous calculation version: %w", err)
	}

	// Рассчитываем новую премию
	calculation, err := h.calculatePremium(policyData)
	if err != nil {
		return fmt.Errorf("failed to calculate premium: %w", err)
	}

	// Сохраняем новый расчёт
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO insurance.premium_calculations 
		(id, policy_id, base_premium, risk_factors, final_premium, calculated_at, calculation_version) 
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.New().String(),
		event.PolicyID,
		calculation.BasePremium,
		calculation.RiskFactorsJSON,
		calculation.FinalPremium,
		time.Now(),
		previousVersion+1,
	)

	if err != nil {
		return fmt.Errorf("failed to save renewed premium calculation: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"policy_id":           event.PolicyID,
		"calculation_version": previousVersion + 1,
		"final_premium":       calculation.FinalPremium,
	}).Info("Premium recalculated for renewal")

	return nil
}

// handlePolicyCancelled обрабатывает отмену полиса
func (h *Handler) handlePolicyCancelled(ctx context.Context, event *kafka.PolicyEvent) error {
	// При отмене полиса логируем событие
	h.logger.WithFields(logrus.Fields{
		"policy_id":    event.PolicyID,
		"cancelled_at": event.Timestamp,
	}).Info("Policy cancelled, no premium calculation needed")

	// В реальной системе здесь может быть логика расчёта возврата премии
	// Пока что просто логируем
	return nil
}

// PremiumCalculation представляет результат расчёта премии
type PremiumCalculation struct {
	BasePremium     float64
	RiskScore       float64
	RiskFactors     map[string]interface{}
	RiskFactorsJSON []byte
	FinalPremium    float64
}

// calculatePremium рассчитывает страховую премию на основе факторов риска
func (h *Handler) calculatePremium(policyData map[string]interface{}) (*PremiumCalculation, error) {
	// Базовая премия
	basePremium := 1000.0

	// Факторы риска
	riskFactors := make(map[string]interface{})
	riskScore := 1.0

	// Возраст водителя
	if age, ok := policyData["driver_age"].(float64); ok {
		riskFactors["driver_age"] = age
		if age < 25 {
			riskScore *= 1.5 // Молодые водители - больший риск
		} else if age > 65 {
			riskScore *= 1.2 // Пожилые водители - повышенный риск
		} else {
			riskScore *= 0.9 // Средний возраст - скидка
		}
	}

	// Стаж вождения
	if experience, ok := policyData["driving_experience"].(float64); ok {
		riskFactors["driving_experience"] = experience
		if experience < 3 {
			riskScore *= 1.3 // Малый стаж - больший риск
		} else if experience > 10 {
			riskScore *= 0.8 // Большой стаж - скидка
		}
	}

	// Тип автомобиля
	if carType, ok := policyData["car_type"].(string); ok {
		riskFactors["car_type"] = carType
		switch carType {
		case "sports":
			riskScore *= 1.8 // Спортивные авто - высокий риск
		case "suv":
			riskScore *= 1.1 // Внедорожники - небольшой риск
		case "sedan":
			riskScore *= 0.9 // Седаны - низкий риск
		case "electric":
			riskScore *= 0.7 // Электромобили - скидка
		}
	}

	// Регион
	if region, ok := policyData["region"].(string); ok {
		riskFactors["region"] = region
		switch region {
		case "moscow":
			riskScore *= 1.4 // Москва - высокий риск
		case "spb":
			riskScore *= 1.2 // СПб - повышенный риск
		default:
			riskScore *= 0.8 // Регионы - скидка
		}
	}

	// История ДТП
	if accidents, ok := policyData["accidents_count"].(float64); ok {
		riskFactors["accidents_count"] = accidents
		riskScore *= math.Pow(1.3, accidents) // Каждое ДТП увеличивает риск на 30%
	}

	// Рассчитываем финальную премию
	finalPremium := basePremium * riskScore

	// Округляем до 2 знаков после запятой
	finalPremium = math.Round(finalPremium*100) / 100

	// Сериализуем факторы риска
	riskFactorsJSON, err := json.Marshal(riskFactors)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal risk factors: %w", err)
	}

	return &PremiumCalculation{
		BasePremium:     basePremium,
		RiskScore:       riskScore,
		RiskFactors:     riskFactors,
		RiskFactorsJSON: riskFactorsJSON,
		FinalPremium:    finalPremium,
	}, nil
}
