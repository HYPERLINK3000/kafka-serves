-- Создание схемы для страховых данных
CREATE SCHEMA IF NOT EXISTS insurance;

-- Таблица для хранения полисов
CREATE TABLE insurance.policies (
    id UUID PRIMARY KEY,
    client_id UUID NOT NULL,
    policy_number VARCHAR(50) UNIQUE NOT NULL,
    policy_type VARCHAR(20) NOT NULL CHECK (policy_type IN ('auto', 'home', 'life')),
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled', 'expired')),
    premium_amount DECIMAL(10,2) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Таблица для событий страховых полисов
CREATE TABLE insurance.policy_events (
    id UUID PRIMARY KEY,
    policy_id UUID NOT NULL REFERENCES insurance.policies(id),
    event_type VARCHAR(20) NOT NULL CHECK (event_type IN ('created', 'renewed', 'cancelled')),
    event_data JSONB,
    processed_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    kafka_offset BIGINT,
    kafka_partition INTEGER,
    kafka_topic VARCHAR(100)
);

-- Таблица для расчётов премий (Underwriting)
CREATE TABLE insurance.premium_calculations (
    id UUID PRIMARY KEY,
    policy_id UUID NOT NULL REFERENCES insurance.policies(id),
    base_premium DECIMAL(10,2) NOT NULL,
    risk_factors JSONB,
    final_premium DECIMAL(10,2) NOT NULL,
    calculated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    calculation_version INTEGER DEFAULT 1
);

-- Таблица для биллинга (Billing)
CREATE TABLE insurance.billing_records (
    id UUID PRIMARY KEY,
    policy_id UUID NOT NULL REFERENCES insurance.policies(id),
    amount DECIMAL(10,2) NOT NULL,
    billing_type VARCHAR(20) NOT NULL CHECK (billing_type IN ('premium', 'refund', 'penalty')),
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed')),
    due_date DATE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    paid_at TIMESTAMP WITH TIME ZONE
);

-- Индексы для производительности
CREATE INDEX idx_policies_client_id ON insurance.policies(client_id);
CREATE INDEX idx_policies_policy_number ON insurance.policies(policy_number);
CREATE INDEX idx_policy_events_policy_id ON insurance.policy_events(policy_id);
CREATE INDEX idx_policy_events_type ON insurance.policy_events(event_type);
CREATE INDEX idx_policy_events_kafka ON insurance.policy_events(kafka_topic, kafka_partition, kafka_offset);
CREATE INDEX idx_premium_calculations_policy_id ON insurance.premium_calculations(policy_id);
CREATE INDEX idx_billing_records_policy_id ON insurance.billing_records(policy_id);
CREATE INDEX idx_billing_records_status ON insurance.billing_records(status);

-- Триггер для обновления updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_policies_updated_at BEFORE UPDATE ON insurance.policies
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Вставка тестовых данных
INSERT INTO insurance.policies (id, client_id, policy_number, policy_type, premium_amount) VALUES
    ('550e8400-e29b-41d4-a716-446655440001', '550e8400-e29b-41d4-a716-446655440101', 'AUTO-2024-001', 'auto', 1200.00),
    ('550e8400-e29b-41d4-a716-446655440002', '550e8400-e29b-41d4-a716-446655440102', 'AUTO-2024-002', 'auto', 1500.00),
    ('550e8400-e29b-41d4-a716-446655440003', '550e8400-e29b-41d4-a716-446655440103', 'AUTO-2024-003', 'auto', 980.00); 