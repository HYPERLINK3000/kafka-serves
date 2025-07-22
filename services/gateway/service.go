package gateway

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/gobulgur/sbs/pkg/kafka"
)

// Service представляет Gateway сервис
type Service struct {
	producer *kafka.Producer
	logger   *logrus.Logger
}

// NewService создаёт новый Gateway сервис
func NewService(producer *kafka.Producer, logger *logrus.Logger) *Service {
	return &Service{
		producer: producer,
		logger:   logger,
	}
}

// CreatePolicyRequest представляет запрос на создание полиса
type CreatePolicyRequest struct {
	ClientID          string `json:"client_id" binding:"required"`
	PolicyType        string `json:"policy_type" binding:"required"`
	DriverAge         int    `json:"driver_age" binding:"required"`
	DrivingExperience int    `json:"driving_experience" binding:"required"`
	CarType           string `json:"car_type" binding:"required"`
	Region            string `json:"region" binding:"required"`
	AccidentsCount    int    `json:"accidents_count"`
}

// CreatePolicy обрабатывает создание нового полиса
func (s *Service) CreatePolicy(c *gin.Context) {
	var req CreatePolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Генерируем ID полиса
	policyID := uuid.New().String()

	// Создаём событие
	event := &kafka.PolicyEvent{
		ID:        uuid.New().String(),
		PolicyID:  policyID,
		EventType: "created",
		EventData: map[string]interface{}{
			"policy": map[string]interface{}{
				"client_id":          req.ClientID,
				"policy_type":        req.PolicyType,
				"driver_age":         float64(req.DriverAge),
				"driving_experience": float64(req.DrivingExperience),
				"car_type":           req.CarType,
				"region":             req.Region,
				"accidents_count":    float64(req.AccidentsCount),
			},
		},
		Source:    "gateway",
		Version:   "1.0",
		Timestamp: time.Now(),
	}

	// Отправляем событие в Kafka
	if err := s.producer.PublishPolicyEvent(c.Request.Context(), event); err != nil {
		s.logger.WithError(err).Error("Failed to publish policy created event")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create policy"})
		return
	}

	s.logger.WithFields(logrus.Fields{
		"policy_id": policyID,
		"client_id": req.ClientID,
		"event_id":  event.ID,
	}).Info("Policy creation event published")

	c.JSON(http.StatusCreated, gin.H{
		"policy_id": policyID,
		"event_id":  event.ID,
		"status":    "created",
	})
}

// RenewPolicyRequest представляет запрос на продление полиса
type RenewPolicyRequest struct {
	DriverAge         *int    `json:"driver_age,omitempty"`
	DrivingExperience *int    `json:"driving_experience,omitempty"`
	CarType           *string `json:"car_type,omitempty"`
	Region            *string `json:"region,omitempty"`
	AccidentsCount    *int    `json:"accidents_count,omitempty"`
}

// RenewPolicy обрабатывает продление полиса
func (s *Service) RenewPolicy(c *gin.Context) {
	policyID := c.Param("id")
	if policyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "policy_id is required"})
		return
	}

	var req RenewPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Создаём данные для продления (только изменённые поля)
	policyData := make(map[string]interface{})

	if req.DriverAge != nil {
		policyData["driver_age"] = float64(*req.DriverAge)
	}
	if req.DrivingExperience != nil {
		policyData["driving_experience"] = float64(*req.DrivingExperience)
	}
	if req.CarType != nil {
		policyData["car_type"] = *req.CarType
	}
	if req.Region != nil {
		policyData["region"] = *req.Region
	}
	if req.AccidentsCount != nil {
		policyData["accidents_count"] = float64(*req.AccidentsCount)
	}

	// Создаём событие продления
	event := &kafka.PolicyEvent{
		ID:        uuid.New().String(),
		PolicyID:  policyID,
		EventType: "renewed",
		EventData: map[string]interface{}{
			"policy": policyData,
		},
		Source:    "gateway",
		Version:   "1.0",
		Timestamp: time.Now(),
	}

	// Отправляем событие в Kafka
	if err := s.producer.PublishPolicyEvent(c.Request.Context(), event); err != nil {
		s.logger.WithError(err).Error("Failed to publish policy renewed event")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to renew policy"})
		return
	}

	s.logger.WithFields(logrus.Fields{
		"policy_id": policyID,
		"event_id":  event.ID,
	}).Info("Policy renewal event published")

	c.JSON(http.StatusOK, gin.H{
		"policy_id": policyID,
		"event_id":  event.ID,
		"status":    "renewed",
	})
}

// CancelPolicy обрабатывает отмену полиса
func (s *Service) CancelPolicy(c *gin.Context) {
	policyID := c.Param("id")
	if policyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "policy_id is required"})
		return
	}

	// Создаём событие отмены
	event := &kafka.PolicyEvent{
		ID:        uuid.New().String(),
		PolicyID:  policyID,
		EventType: "cancelled",
		EventData: map[string]interface{}{
			"reason": "user_request",
		},
		Source:    "gateway",
		Version:   "1.0",
		Timestamp: time.Now(),
	}

	// Отправляем событие в Kafka
	if err := s.producer.PublishPolicyEvent(c.Request.Context(), event); err != nil {
		s.logger.WithError(err).Error("Failed to publish policy cancelled event")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cancel policy"})
		return
	}

	s.logger.WithFields(logrus.Fields{
		"policy_id": policyID,
		"event_id":  event.ID,
	}).Info("Policy cancellation event published")

	c.JSON(http.StatusOK, gin.H{
		"policy_id": policyID,
		"event_id":  event.ID,
		"status":    "cancelled",
	})
}

// GetPolicy возвращает информацию о полисе
func (s *Service) GetPolicy(c *gin.Context) {
	policyID := c.Param("id")
	if policyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "policy_id is required"})
		return
	}

	// В реальной системе здесь был бы запрос к базе данных
	// Для демонстрации возвращаем заглушку
	c.JSON(http.StatusOK, gin.H{
		"policy_id": policyID,
		"status":    "active",
		"message":   "Policy details would be fetched from database",
	})
}

// GetClientPolicies возвращает список полисов клиента
func (s *Service) GetClientPolicies(c *gin.Context) {
	clientID := c.Param("id")
	if clientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "client_id is required"})
		return
	}

	// В реальной системе здесь был бы запрос к базе данных
	// Для демонстрации возвращаем заглушку
	c.JSON(http.StatusOK, gin.H{
		"client_id": clientID,
		"policies":  []string{},
		"message":   "Client policies would be fetched from database",
	})
}
