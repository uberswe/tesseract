-- The service keeps every owner's tesseract inventory in memory and periodically
-- flushes it here (and reloads it on demand). One row per owner UUID; the blob is
-- the gzipped NBT snapshot of the shared inventory.
CREATE TABLE IF NOT EXISTS tesseract_inventories (
    owner_uuid     UUID PRIMARY KEY,
    inventory_data BYTEA NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
