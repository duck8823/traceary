SELECT observation.observation_id,
       observation.observed_at,
       observation.host,
       COALESCE(observation.provider, ''),
       COALESCE(observation.model, ''),
       observation.accounting,
       observation.terminal_code,
       observation.input_state,
       observation.input_tokens,
       observation.cached_input_state,
       observation.cached_input_tokens,
       observation.cache_write_input_state,
       observation.cache_write_input_tokens,
       observation.output_state,
       observation.output_tokens,
       observation.reasoning_output_state,
       observation.reasoning_output_tokens,
       observation.total_state,
       observation.total_tokens,
       observation.cost_state,
       observation.cost_amount_micros,
       COALESCE(observation.cost_currency, ''),
       COALESCE(observation.cost_origin, ''),
       COALESCE(observation.price_table_version, ''),
       COALESCE(attribution.run_host, ''),
       COALESCE(attribution.run_id, ''),
       COALESCE(lineage.repository, ''),
       COALESCE(lineage.ticket_ref, ''),
       lineage.pull_request_number,
       COALESCE(lineage.batch_id, ''),
       lineage.packet_bytes,
       lineage.tool_output_bytes
  FROM usage_observations AS observation
  LEFT JOIN usage_observation_runs AS attribution
    ON attribution.observation_id = observation.observation_id
  LEFT JOIN run_lineages AS lineage
    ON lineage.host = attribution.run_host
   AND lineage.run_id = attribution.run_id
  LEFT JOIN sessions AS session
    ON session.session_id = observation.session_id
 WHERE observation.status = 'finalized'
   AND (
        observation.accounting != 'latest_snapshot'
        OR NOT EXISTS (
            SELECT 1
             FROM usage_observations AS successor
             WHERE successor.supersedes_id = observation.observation_id
               AND successor.status = 'finalized'
        )
   )
   AND (? = '' OR session.workspace = ?)
   AND (? = '' OR session.client = ?)
   AND (? = '' OR ts_norm(observation.observed_at) >= ts_norm(?))
   AND (? = '' OR ts_norm(observation.observed_at) < ts_norm(?))
 ORDER BY ts_norm(observation.observed_at) DESC, observation.observation_id DESC
 LIMIT ? OFFSET ?
