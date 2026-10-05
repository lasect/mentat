SELECT pid, queryid, left(event_type, 128) AS event_type,
       left(event, 128) AS event, count
FROM pg_wait_sampling_profile
ORDER BY count DESC NULLS LAST, pid, queryid, event_type, event
LIMIT 1000;
