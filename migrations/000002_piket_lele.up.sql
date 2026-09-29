-- Migration 000002: Piket Lele (Catfish Feeding Duty Roster)

CREATE TABLE IF NOT EXISTS piket_slots (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    rotation_order INT NOT NULL UNIQUE,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS piket_slot_members (
    id BIGSERIAL PRIMARY KEY,
    slot_id BIGINT NOT NULL REFERENCES piket_slots(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    phone_number VARCHAR(50) NOT NULL,
    whatsapp_jid VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS piket_logs (
    id BIGSERIAL PRIMARY KEY,
    feeding_date DATE NOT NULL UNIQUE,
    slot_id BIGINT REFERENCES piket_slots(id) ON DELETE SET NULL,
    assigned_members_display TEXT NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    fed_at TIMESTAMPTZ,
    proof_file_path VARCHAR(500),
    confirmed_by VARCHAR(255),
    early_reminded_at TIMESTAMPTZ,
    feeding_reminded_at TIMESTAMPTZ,
    overdue_reminded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_piket_slots_active_order ON piket_slots(active, rotation_order);
CREATE INDEX IF NOT EXISTS idx_piket_logs_date ON piket_logs(feeding_date);
