package domain

import "time"

type Plan struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Price           int64     `json:"price"` // in IDR (e.g. 23000)
	BillingInterval string    `json:"billing_interval"` // "MONTHLY"
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
