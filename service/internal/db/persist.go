package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/uberswe/tesseract/internal/inventory"
	"github.com/uberswe/tesseract/internal/protocol"
)

type Persister struct {
	pool  *pgxpool.Pool
	store *inventory.Store
}

func NewPersister(pool *pgxpool.Pool, store *inventory.Store) *Persister {
	return &Persister{pool: pool, store: store}
}

func (p *Persister) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.SaveDirty(ctx)
		}
	}
}

func (p *Persister) SaveDirty(ctx context.Context) {
	dirty := p.store.DrainDirty()
	if len(dirty) == 0 {
		return
	}
	saved := 0
	for uuid, entry := range dirty {
		if err := p.save(ctx, uuid, entry.Data); err != nil {
			slog.Error("failed to persist inventory", "uuid", uuid.String(), "error", err)
			continue
		}
		saved++
	}
	slog.Info("persisted inventories", "count", saved)
}

func (p *Persister) SaveAll(ctx context.Context) {
	all := p.store.FlushAll()
	if len(all) == 0 {
		return
	}
	saved := 0
	for uuid, entry := range all {
		if err := p.save(ctx, uuid, entry.Data); err != nil {
			slog.Error("failed to persist inventory on shutdown", "uuid", uuid.String(), "error", err)
			continue
		}
		saved++
	}
	slog.Info("persisted all inventories on shutdown", "count", saved)
}

func (p *Persister) Load(ctx context.Context, uuid protocol.UUID) ([]byte, int64, error) {
	sql := `SELECT inventory_data, EXTRACT(EPOCH FROM updated_at)::bigint * 1000
		FROM tesseract_inventories
		WHERE owner_uuid = $1`
	uuidStr := uuid.String()
	var data []byte
	var ts int64
	err := p.pool.QueryRow(ctx, sql, uuidStr).Scan(&data, &ts)
	if err != nil {
		return nil, 0, fmt.Errorf("load inventory: %w", err)
	}
	return data, ts, nil
}

func (p *Persister) save(ctx context.Context, uuid protocol.UUID, data []byte) error {
	sql := `INSERT INTO tesseract_inventories (owner_uuid, inventory_data, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (owner_uuid) DO UPDATE SET inventory_data = EXCLUDED.inventory_data, updated_at = NOW()`
	uuidStr := uuid.String()
	_, err := p.pool.Exec(ctx, sql, uuidStr, data)
	if err != nil {
		return fmt.Errorf("save inventory: %w", err)
	}
	return nil
}
