SELECT ash_time, datid, pid, usesysid, backend_start, xact_start, query_start, state_change,
       queryid, left(application_name, 256) AS application_name,
       left(wait_event_type, 128) AS wait_event_type, left(wait_event, 128) AS wait_event,
       left(state, 128) AS state, left(backend_type, 128) AS backend_type,
       left(query, 4096) AS query, left(top_level_query, 4096) AS top_level_query,
       left(cmdtype, 32) AS cmdtype, blockers, blockerpid,
       left(blocker_state, 128) AS blocker_state
FROM pg_active_session_history
WHERE datid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
  AND ash_time >= statement_timestamp() - INTERVAL '5 minutes'
  AND ash_time <= statement_timestamp()
ORDER BY ash_time DESC, pid, backend_start, queryid
LIMIT 1000;
