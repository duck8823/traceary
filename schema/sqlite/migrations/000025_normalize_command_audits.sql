ALTER TABLE command_audits ADD COLUMN command_wrapper TEXT NOT NULL DEFAULT '';
ALTER TABLE command_audits ADD COLUMN command_name TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE command_audits ADD COLUMN failure_reason TEXT NOT NULL DEFAULT 'unknown'
    CHECK (failure_reason IN ('unknown', 'none', 'exit_code', 'signal', 'timeout', 'hook_denied', 'host_error'));
