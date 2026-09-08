package storage

import (
	"context"
	"fmt"

	"wabill/internal/domain"
)

type AuditRepo struct {
	db *DB
}

func NewAuditRepo(db *DB) *AuditRepo {
	return &AuditRepo{db: db}
}

func (r *AuditRepo) Record(ctx context.Context, log *domain.AuditLog) error {
	query := `
		INSERT INTO audit_logs (actor, action, entity_type, entity_id, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, NOW())
		RETURNING id, created_at
	`
	meta := log.Metadata
	if meta == "" {
		meta = "{}"
	}
	err := r.db.QueryRowContext(ctx, query, log.Actor, log.Action, log.EntityType, log.EntityID, meta).
		Scan(&log.ID, &log.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to record audit log: %w", err)
	}
	return nil
}
