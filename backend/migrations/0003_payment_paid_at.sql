-- Store the time revenue is actually received, separate from payment initiation.
ALTER TABLE payments ADD COLUMN paid_at TIMESTAMPTZ;
UPDATE payments SET paid_at = created_at WHERE status = 'paid';
ALTER TABLE payments ADD CONSTRAINT payments_paid_at_check CHECK (status <> 'paid' OR paid_at IS NOT NULL);
