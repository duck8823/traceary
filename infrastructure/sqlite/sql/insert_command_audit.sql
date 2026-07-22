INSERT INTO command_audits(event_id, command_text, command_wrapper, command_name, input_text, output_text, input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed, failure_reason)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
