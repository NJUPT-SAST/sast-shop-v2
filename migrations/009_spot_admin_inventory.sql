BEGIN;

ALTER TABLE spot.spot_order ADD COLUMN inventory_state TEXT NOT NULL DEFAULT 'reserved'
    CHECK (inventory_state IN ('reserved', 'released', 'unverified'));

UPDATE spot.spot_order so SET inventory_state = CASE WHEN EXISTS (
    SELECT 1 FROM spot.spot_stock_ledger ledger
    WHERE ledger.ref_type = 'spot_order' AND ledger.ref_id = so.id
      AND ledger.reason = 'order_cancel' AND ledger.delta = so.quantity
) THEN 'released' ELSE 'unverified' END
WHERE so.status = 'cancelled';

COMMIT;
