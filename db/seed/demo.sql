-- Deterministic demo catalog: a small coffee bar priced in USD cents.
-- prd_croissant has low stock so the insufficient-stock path is easy to demo.
-- prd_seasonal_latte is inactive and must not appear in catalog.listProducts.

INSERT INTO products (id, sku, name, category, price_minor, currency, active) VALUES
  ('prd_espresso',       'COF-001', 'Espresso',           'Coffee', 300, 'USD', true),
  ('prd_flat_white',     'COF-002', 'Flat White',         'Coffee', 450, 'USD', true),
  ('prd_cappuccino',     'COF-003', 'Cappuccino',         'Coffee', 450, 'USD', true),
  ('prd_cold_brew',      'COF-004', 'Cold Brew',          'Coffee', 500, 'USD', true),
  ('prd_matcha_latte',   'TEA-001', 'Matcha Latte',       'Tea',    525, 'USD', true),
  ('prd_croissant',      'BAK-001', 'Butter Croissant',   'Bakery', 375, 'USD', true),
  ('prd_banana_bread',   'BAK-002', 'Banana Bread',       'Bakery', 400, 'USD', true),
  ('prd_seasonal_latte', 'COF-099', 'Pumpkin Spice Latte','Coffee', 600, 'USD', false);

-- restock_to is used only by the live demo's replenishment job: once a product is
-- down to a quarter of it, the job tops it back up. The croissant restocks to 3, so
-- asking for 4 still shows the insufficient-stock error.
INSERT INTO inventory (product_id, available, restock_to) VALUES
  ('prd_espresso',       200, 200),
  ('prd_flat_white',     150, 150),
  ('prd_cappuccino',     150, 150),
  ('prd_cold_brew',       40,  40),
  ('prd_matcha_latte',    60,  60),
  ('prd_croissant',        3,   3),
  ('prd_banana_bread',    12,  12),
  ('prd_seasonal_latte',   0, NULL);
