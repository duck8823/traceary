ALTER TABLE command_audits ADD COLUMN input_original_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE command_audits ADD COLUMN output_original_bytes INTEGER NOT NULL DEFAULT 0;
