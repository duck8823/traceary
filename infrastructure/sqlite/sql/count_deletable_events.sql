SELECT COUNT(*) FROM events WHERE ts_norm(created_at) < ts_norm(?)
