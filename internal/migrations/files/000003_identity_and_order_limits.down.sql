ALTER TABLE orders ALTER COLUMN number TYPE TEXT;
ALTER TABLE withdrawals ALTER COLUMN order_number TYPE TEXT;

ALTER TABLE users ALTER COLUMN id DROP IDENTITY IF EXISTS;
CREATE SEQUENCE users_id_seq;
ALTER SEQUENCE users_id_seq OWNED BY users.id;
ALTER TABLE users ALTER COLUMN id SET DEFAULT nextval('users_id_seq'::regclass);
SELECT setval('users_id_seq', COALESCE((SELECT MAX(id) FROM users), 1), (SELECT MAX(id) IS NOT NULL FROM users));

ALTER TABLE orders ALTER COLUMN id DROP IDENTITY IF EXISTS;
CREATE SEQUENCE orders_id_seq;
ALTER SEQUENCE orders_id_seq OWNED BY orders.id;
ALTER TABLE orders ALTER COLUMN id SET DEFAULT nextval('orders_id_seq'::regclass);
SELECT setval('orders_id_seq', COALESCE((SELECT MAX(id) FROM orders), 1), (SELECT MAX(id) IS NOT NULL FROM orders));

ALTER TABLE withdrawals ALTER COLUMN id DROP IDENTITY IF EXISTS;
CREATE SEQUENCE withdrawals_id_seq;
ALTER SEQUENCE withdrawals_id_seq OWNED BY withdrawals.id;
ALTER TABLE withdrawals ALTER COLUMN id SET DEFAULT nextval('withdrawals_id_seq'::regclass);
SELECT setval('withdrawals_id_seq', COALESCE((SELECT MAX(id) FROM withdrawals), 1), (SELECT MAX(id) IS NOT NULL FROM withdrawals));
