UPDATE sessions
   SET model = ?
 WHERE session_id = ?
   AND (model IS NULL OR model = '')
   AND ? <> ''
