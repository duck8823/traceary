SELECT session_id, started_at, ended_at, client, agent, workspace, label, summary, COALESCE(parent_session_id, '') FROM sessions WHERE session_id = ?
