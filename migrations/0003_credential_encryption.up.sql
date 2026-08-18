-- provider_credentials.credential is now envelope-encrypted: each row gets
-- its own AES-256-GCM Data Encryption Key (DEK), which wraps the actual
-- credential; wrapped_dek stores that DEK, itself wrapped by APP_MASTER_KEY.
-- credential switches from jsonb to bytea: AES-GCM ciphertext is arbitrary
-- binary, not valid JSON/UTF8 text.
alter table provider_credentials alter column credential type bytea using credential::text::bytea;
alter table provider_credentials add column wrapped_dek bytea not null;
