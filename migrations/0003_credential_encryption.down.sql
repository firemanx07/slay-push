alter table provider_credentials drop column if exists wrapped_dek;
alter table provider_credentials alter column credential type jsonb using credential::text::jsonb;
