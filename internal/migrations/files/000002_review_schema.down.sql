ALTER TABLE orders
    ALTER COLUMN status TYPE TEXT USING status::text;

DROP TYPE order_status;

ALTER TABLE users
    ALTER COLUMN login TYPE TEXT,
    ALTER COLUMN password_hash TYPE TEXT;
