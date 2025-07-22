package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Shopify/sarama"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/gobulgur/sbs/pkg/kafka"
)

// Handler обрабатывает события для биллинга и финансовых операций
type Handler struct {
	db     *sql.DB
	logger *logrus.Logger
}

// NewHandler создаёт новый handler для billing
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
	}).Info("Processing billing event")

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

// handlePolicyCreated создаёт счёт на оплату для нового полиса
func (h *Handler) handlePolicyCreated(ctx context.Context, event *kafka.PolicyEvent) error {
	// Получаем рассчитанную премию из базы данных
	var finalPremium float64
	err := h.db.QueryRowContext(ctx, `
		SELECT final_premium 
		FROM insurance.premium_calculations 
		WHERE policy_id = $1 
		ORDER BY calculated_at DESC 
		LIMIT 1`,
		event.PolicyID,
	).Scan(&finalPremium)

	if err != nil {
		if err == sql.ErrNoRows {
			// Премия ещё не рассчитана, пропускаем пока
			h.logger.WithField("policy_id", event.PolicyID).Warn("Premium not calculated yet, skipping billing")
			return nil
		}
		return fmt.Errorf("failed to get premium calculation: %w", err)
	}

	// Создаём счёт на оплату
	billingRecord := &BillingRecord{
		ID:          uuid.New().String(),
		PolicyID:    event.PolicyID,
		Amount:      finalPremium,
		BillingType: "premium",
		Status:      "pending",
		DueDate:     time.Now().AddDate(0, 0, 30), // 30 дней на оплату
		CreatedAt:   time.Now(),
	}

	err = h.saveBillingRecord(ctx, billingRecord)
	if err != nil {
		return fmt.Errorf("failed to save billing record: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"policy_id":  event.PolicyID,
		"billing_id": billingRecord.ID,
		"amount":     billingRecord.Amount,
		"due_date":   billingRecord.DueDate,
	}).Info("Billing record created for new policy")

	// Симулируем отправку уведомления клиенту
	h.sendPaymentNotification(billingRecord)

	return nil
}

// handlePolicyRenewed создаёт счёт для продления полиса
func (h *Handler) handlePolicyRenewed(ctx context.Context, event *kafka.PolicyEvent) error {
	// Получаем новую рассчитанную премию
	var finalPremium float64
	err := h.db.QueryRowContext(ctx, `
		SELECT final_premium 
		FROM insurance.premium_calculations 
		WHERE policy_id = $1 
		ORDER BY calculated_at DESC 
		LIMIT 1`,
		event.PolicyID,
	).Scan(&finalPremium)

	if err != nil {
		if err == sql.ErrNoRows {
			h.logger.WithField("policy_id", event.PolicyID).Warn("Premium not calculated for renewal, skipping billing")
			return nil
		}
		return fmt.Errorf("failed to get renewal premium calculation: %w", err)
	}

	// Создаём счёт на продление
	billingRecord := &BillingRecord{
		ID:          uuid.New().String(),
		PolicyID:    event.PolicyID,
		Amount:      finalPremium,
		BillingType: "premium",
		Status:      "pending",
		DueDate:     time.Now().AddDate(0, 0, 15), // 15 дней на оплату продления
		CreatedAt:   time.Now(),
	}

	err = h.saveBillingRecord(ctx, billingRecord)
	if err != nil {
		return fmt.Errorf("failed to save renewal billing record: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"policy_id":    event.PolicyID,
		"billing_id":   billingRecord.ID,
		"amount":       billingRecord.Amount,
		"billing_type": "renewal",
	}).Info("Billing record created for policy renewal")

	h.sendPaymentNotification(billingRecord)

	return nil
}

// handlePolicyCancelled обрабатывает отмену полиса и возврат средств
func (h *Handler) handlePolicyCancelled(ctx context.Context, event *kafka.PolicyEvent) error {
	// Получаем информацию о последней оплаченной премии
	var lastBillingID string
	var lastAmount float64
	var paidAt sql.NullTime

	err := h.db.QueryRowContext(ctx, `
		SELECT id, amount, paid_at
		FROM insurance.billing_records 
		WHERE policy_id = $1 AND status = 'paid' AND billing_type = 'premium'
		ORDER BY created_at DESC 
		LIMIT 1`,
		event.PolicyID,
	).Scan(&lastBillingID, &lastAmount, &paidAt)

	if err != nil {
		if err == sql.ErrNoRows {
			h.logger.WithField("policy_id", event.PolicyID).Info("No paid premiums found, no refund needed")
			return nil
		}
		return fmt.Errorf("failed to get last billing record: %w", err)
	}

	// Рассчитываем возврат (пропорционально оставшемуся времени)
	refundAmount := h.calculateRefund(lastAmount, paidAt.Time, event.Timestamp)

	if refundAmount <= 0 {
		h.logger.WithField("policy_id", event.PolicyID).Info("No refund amount, policy period expired")
		return nil
	}

	// Создаём запись о возврате
	refundRecord := &BillingRecord{
		ID:          uuid.New().String(),
		PolicyID:    event.PolicyID,
		Amount:      refundAmount,
		BillingType: "refund",
		Status:      "pending",
		CreatedAt:   time.Now(),
	}

	err = h.saveBillingRecord(ctx, refundRecord)
	if err != nil {
		return fmt.Errorf("failed to save refund record: %w", err)
	}

	// Симулируем обработку возврата
	err = h.processRefund(ctx, refundRecord)
	if err != nil {
		return fmt.Errorf("failed to process refund: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"policy_id":       event.PolicyID,
		"refund_id":       refundRecord.ID,
		"refund_amount":   refundAmount,
		"original_amount": lastAmount,
	}).Info("Refund processed for cancelled policy")

	return nil
}

// BillingRecord представляет запись о биллинге
type BillingRecord struct {
	ID          string
	PolicyID    string
	Amount      float64
	BillingType string // premium, refund, penalty
	Status      string // pending, paid, failed
	DueDate     time.Time
	CreatedAt   time.Time
	PaidAt      *time.Time
}

// saveBillingRecord сохраняет запись о биллинге в базу данных
func (h *Handler) saveBillingRecord(ctx context.Context, record *BillingRecord) error {
	var dueDate interface{}
	if !record.DueDate.IsZero() {
		dueDate = record.DueDate
	}

	var paidAt interface{}
	if record.PaidAt != nil {
		paidAt = *record.PaidAt
	}

	_, err := h.db.ExecContext(ctx, `
		INSERT INTO insurance.billing_records 
		(id, policy_id, amount, billing_type, status, due_date, created_at, paid_at) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		record.ID,
		record.PolicyID,
		record.Amount,
		record.BillingType,
		record.Status,
		dueDate,
		record.CreatedAt,
		paidAt,
	)

	return err
}

// calculateRefund рассчитывает сумму возврата на основе оставшегося времени
func (h *Handler) calculateRefund(originalAmount float64, paidAt time.Time, cancelledAt time.Time) float64 {
	// Предполагаем, что полис действует 1 год
	policyDuration := 365 * 24 * time.Hour

	// Время с момента оплаты до отмены
	timeUsed := cancelledAt.Sub(paidAt)

	if timeUsed >= policyDuration {
		return 0 // Полис уже истёк
	}

	// Пропорциональный возврат за неиспользованное время
	unusedRatio := float64(policyDuration-timeUsed) / float64(policyDuration)
	refundAmount := originalAmount * unusedRatio

	// Округляем до 2 знаков после запятой
	return float64(int(refundAmount*100)) / 100
}

// processRefund обрабатывает возврат средств
func (h *Handler) processRefund(ctx context.Context, refundRecord *BillingRecord) error {
	// Симулируем обработку возврата
	time.Sleep(100 * time.Millisecond)

	// Обновляем статус возврата
	now := time.Now()
	_, err := h.db.ExecContext(ctx, `
		UPDATE insurance.billing_records 
		SET status = 'paid', paid_at = $1 
		WHERE id = $2`,
		now,
		refundRecord.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update refund status: %w", err)
	}

	refundRecord.Status = "paid"
	refundRecord.PaidAt = &now

	return nil
}

// sendPaymentNotification отправляет уведомление о необходимости оплаты
func (h *Handler) sendPaymentNotification(record *BillingRecord) {
	// В реальной системе здесь была бы отправка email/SMS
	h.logger.WithFields(logrus.Fields{
		"policy_id":  record.PolicyID,
		"billing_id": record.ID,
		"amount":     record.Amount,
		"due_date":   record.DueDate,
	}).Info("Payment notification sent (simulated)")
}
