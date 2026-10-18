-- Level the demo replenishment job restocks a product to. NULL means never restock.
-- See docs/plan.md section 11 and docs/deploy.md.
ALTER TABLE inventory ADD COLUMN restock_to integer CHECK (restock_to > 0);
