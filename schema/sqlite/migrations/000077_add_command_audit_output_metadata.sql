-- #2267: read-only tool audits store access facts instead of output text.
-- One nullable TEXT column. No scan, no backfill, no rewrite of existing rows:
-- historical rows keep their captured output_text and stay NULL here.
ALTER TABLE command_audits ADD COLUMN output_metadata TEXT;
