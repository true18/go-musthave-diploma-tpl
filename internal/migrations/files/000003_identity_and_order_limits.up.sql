ALTER TABLE users ALTER COLUMN id DROP DEFAULT;
ALTER SEQUENCE users_id_seq OWNED BY NONE;
DROP SEQUENCE users_id_seq;
ALTER TABLE users ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
SELECT setval(pg_get_serial_sequence('users', 'id'), COALESCE((SELECT MAX(id) FROM users), 1), (SELECT MAX(id) IS NOT NULL FROM users));

ALTER TABLE orders ALTER COLUMN id DROP DEFAULT;
ALTER SEQUENCE orders_id_seq OWNED BY NONE;
DROP SEQUENCE orders_id_seq;
ALTER TABLE orders ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
SELECT setval(pg_get_serial_sequence('orders', 'id'), COALESCE((SELECT MAX(id) FROM orders), 1), (SELECT MAX(id) IS NOT NULL FROM orders));

ALTER TABLE withdrawals ALTER COLUMN id DROP DEFAULT;
ALTER SEQUENCE withdrawals_id_seq OWNED BY NONE;
DROP SEQUENCE withdrawals_id_seq;
ALTER TABLE withdrawals ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
SELECT setval(pg_get_serial_sequence('withdrawals', 'id'), COALESCE((SELECT MAX(id) FROM withdrawals), 1), (SELECT MAX(id) IS NOT NULL FROM withdrawals));

ALTER TABLE orders ALTER COLUMN number TYPE VARCHAR(255);
ALTER TABLE withdrawals ALTER COLUMN order_number TYPE VARCHAR(255);
