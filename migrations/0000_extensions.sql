-- Enable pgcrypto for gen_random_uuid() support across PostgreSQL versions.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
