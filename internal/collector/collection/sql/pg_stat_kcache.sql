SELECT dbid, userid, queryid, top,
       plan_reads, plan_writes, plan_user_time, plan_system_time,
       plan_minflts, plan_majflts, plan_nswaps, plan_msgsnds, plan_msgrcvs,
       plan_nsignals, plan_nvcsws, plan_nivcsws,
       exec_reads, exec_writes, exec_user_time, exec_system_time,
       exec_minflts, exec_majflts, exec_nswaps, exec_msgsnds, exec_msgrcvs,
       exec_nsignals, exec_nvcsws, exec_nivcsws
FROM pg_stat_kcache()
WHERE dbid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
ORDER BY (plan_user_time + plan_system_time + exec_user_time + exec_system_time) DESC NULLS LAST,
         queryid, userid, top
LIMIT 1000;
