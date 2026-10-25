ALTER TABLE users
    ALTER COLUMN login TYPE VARCHAR(255),
    ALTER COLUMN password_hash TYPE VARCHAR(255);

CREATE TYPE order_status AS ENUM ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED');

ALTER TABLE orders
    ALTER COLUMN status TYPE order_status USING status::order_status;
