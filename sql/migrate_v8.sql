ALTER TABLE emails
    ADD COLUMN IF NOT EXISTS parsed_code_source VARCHAR(32) NOT NULL DEFAULT '';

ALTER TABLE emails
    ADD COLUMN IF NOT EXISTS parsed_link TEXT NOT NULL DEFAULT '';

ALTER TABLE emails
    ADD COLUMN IF NOT EXISTS parsed_link_source VARCHAR(32) NOT NULL DEFAULT '';

UPDATE emails
SET parsed_code_source = CASE
        WHEN parsed_code <> '' AND parsed_code_source = '' THEN 'stored'
        ELSE parsed_code_source
    END
WHERE parsed_code <> '';
