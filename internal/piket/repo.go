package piket

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"wabill/internal/storage"
)

type Repo struct {
	db *storage.DB
}

func NewRepo(db *storage.DB) *Repo {
	return &Repo{db: db}
}

// CreateSlot creates a new slot in rotation with its assigned members.
func (r *Repo) CreateSlot(ctx context.Context, name string, members []Member) (*Slot, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	// Find the next rotation order
	var maxOrder sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT MAX(rotation_order) FROM piket_slots").Scan(&maxOrder)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to query max rotation_order: %w", err)
	}
	nextOrder := 1
	if maxOrder.Valid {
		nextOrder = int(maxOrder.Int64) + 1
	}

	slot := &Slot{
		Name:          name,
		RotationOrder: nextOrder,
		Active:        true,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO piket_slots (name, rotation_order, active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, slot.Name, slot.RotationOrder, slot.Active, slot.CreatedAt, slot.UpdatedAt).Scan(&slot.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to insert slot: %w", err)
	}

	for i := range members {
		m := &members[i]
		m.SlotID = slot.ID
		m.CreatedAt = time.Now().UTC()
		err = tx.QueryRowContext(ctx, `
			INSERT INTO piket_slot_members (slot_id, name, phone_number, whatsapp_jid, created_at)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id
		`, m.SlotID, m.Name, m.PhoneNumber, m.WhatsAppJID, m.CreatedAt).Scan(&m.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to insert slot member: %w", err)
		}
	}
	slot.Members = members

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit tx: %w", err)
	}

	return slot, nil
}

// ListActiveSlots returns all active slots ordered by rotation_order.
func (r *Repo) ListActiveSlots(ctx context.Context) ([]Slot, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, rotation_order, active, created_at, updated_at
		FROM piket_slots
		WHERE active = true
		ORDER BY rotation_order ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query active slots: %w", err)
	}
	defer rows.Close()

	var slots []Slot
	for rows.Next() {
		var s Slot
		if err := rows.Scan(&s.ID, &s.Name, &s.RotationOrder, &s.Active, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		slots = append(slots, s)
	}

	for i := range slots {
		mRows, err := r.db.QueryContext(ctx, `
			SELECT id, slot_id, name, phone_number, whatsapp_jid, created_at
			FROM piket_slot_members
			WHERE slot_id = $1
			ORDER BY id ASC
		`, slots[i].ID)
		if err != nil {
			return nil, err
		}
		for mRows.Next() {
			var m Member
			if err := mRows.Scan(&m.ID, &m.SlotID, &m.Name, &m.PhoneNumber, &m.WhatsAppJID, &m.CreatedAt); err != nil {
				mRows.Close()
				return nil, err
			}
			slots[i].Members = append(slots[i].Members, m)
		}
		mRows.Close()
	}

	return slots, nil
}

// DeleteSlot deletes a slot and its members by ID.
func (r *Repo) DeleteSlot(ctx context.Context, slotID int64) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM piket_slots WHERE id = $1", slotID)
	return err
}

// GetLogByDate returns the feeding log for the specified date string (YYYY-MM-DD).
func (r *Repo) GetLogByDate(ctx context.Context, dateStr string) (*Log, error) {
	var l Log
	var fedAt, earlyAt, feedAt, overAt sql.NullTime
	var proofPath, confBy sql.NullString
	var slotID sql.NullInt64

	err := r.db.QueryRowContext(ctx, `
		SELECT id, feeding_date, slot_id, assigned_members_display, status,
		       fed_at, proof_file_path, confirmed_by,
		       early_reminded_at, feeding_reminded_at, overdue_reminded_at,
		       created_at, updated_at
		FROM piket_logs
		WHERE feeding_date = $1
	`, dateStr).Scan(
		&l.ID, &l.FeedingDate, &slotID, &l.AssignedMembersDisplay, &l.Status,
		&fedAt, &proofPath, &confBy,
		&earlyAt, &feedAt, &overAt,
		&l.CreatedAt, &l.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query log by date: %w", err)
	}

	if slotID.Valid {
		l.SlotID = &slotID.Int64
	}
	if fedAt.Valid {
		l.FedAt = &fedAt.Time
	}
	if proofPath.Valid {
		l.ProofFilePath = proofPath.String
	}
	if confBy.Valid {
		l.ConfirmedBy = confBy.String
	}
	if earlyAt.Valid {
		l.EarlyRemindedAt = &earlyAt.Time
	}
	if feedAt.Valid {
		l.FeedingRemindedAt = &feedAt.Time
	}
	if overAt.Valid {
		l.OverdueRemindedAt = &overAt.Time
	}

	return &l, nil
}

// CreateLog records a new daily feeding schedule.
func (r *Repo) CreateLog(ctx context.Context, l *Log) error {
	now := time.Now().UTC()
	l.CreatedAt = now
	l.UpdatedAt = now

	return r.db.QueryRowContext(ctx, `
		INSERT INTO piket_logs (
			feeding_date, slot_id, assigned_members_display, status,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, l.FeedingDate, l.SlotID, l.AssignedMembersDisplay, string(l.Status), l.CreatedAt, l.UpdatedAt).Scan(&l.ID)
}

// MarkDone updates the daily log status to DONE.
func (r *Repo) MarkDone(ctx context.Context, dateStr string, confirmedBy string, proofPath string, fedAt time.Time) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE piket_logs
		SET status = 'DONE',
		    confirmed_by = $2,
		    proof_file_path = $3,
		    fed_at = $4,
		    updated_at = $5
		WHERE feeding_date = $1
	`, dateStr, confirmedBy, proofPath, fedAt, now)
	return err
}

// UpdateAssignedMembers updates today's assigned members display string.
func (r *Repo) UpdateAssignedMembers(ctx context.Context, dateStr string, display string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE piket_logs
		SET assigned_members_display = $2,
		    updated_at = $3
		WHERE feeding_date = $1
	`, dateStr, display, now)
	return err
}

// SetEarlyReminded marks that the early (15:00) reminder has been sent.
func (r *Repo) SetEarlyReminded(ctx context.Context, dateStr string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE piket_logs
		SET early_reminded_at = $2,
		    updated_at = $2
		WHERE feeding_date = $1
	`, dateStr, now)
	return err
}

// SetFeedingReminded marks that the feeding time (16:30) reminder has been sent.
func (r *Repo) SetFeedingReminded(ctx context.Context, dateStr string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE piket_logs
		SET feeding_reminded_at = $2,
		    updated_at = $2
		WHERE feeding_date = $1
	`, dateStr, now)
	return err
}

// SetOverdueReminded marks that the overdue (17:30) reminder has been sent.
func (r *Repo) SetOverdueReminded(ctx context.Context, dateStr string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE piket_logs
		SET overdue_reminded_at = $2,
		    updated_at = $2
		WHERE feeding_date = $1
	`, dateStr, now)
	return err
}
