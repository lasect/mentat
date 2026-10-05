SELECT bucket, bucket_start_time, bucket_done,
       dbid, userid, queryid, pgsm_query_id, top_queryid, planid, toplevel,
       client_ip, left(application_name, 256) AS application_name,
       left(query, 4096) AS query,
       cmd_type, elevel, left(sqlcode, 5) AS sqlcode,
       calls, rows, plans,
       total_exec_time, min_exec_time, max_exec_time, mean_exec_time, stddev_exec_time,
       total_plan_time, min_plan_time, max_plan_time, mean_plan_time, stddev_plan_time,
       shared_blks_hit, shared_blks_read, shared_blks_dirtied, shared_blks_written,
       local_blks_hit, local_blks_read, local_blks_dirtied, local_blks_written,
       temp_blks_read, temp_blks_written, blk_read_time, blk_write_time,
       cpu_user_time, cpu_sys_time, wal_records, wal_fpi, wal_bytes
FROM pg_stat_monitor
WHERE dbid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
ORDER BY bucket_start_time DESC NULLS LAST, total_exec_time DESC NULLS LAST,
         queryid, userid, planid, client_ip, application_name, pgsm_query_id,
         top_queryid, toplevel, elevel, sqlcode
LIMIT 1000;
