SELECT dbid, userid, queryid, qualid, uniquequalid, qualnodeid, uniquequalnodeid,
       lrelid, lattnum, opno, rrelid, rattnum,
       occurences, execution_count, nbfiltered,
       min_err_estimate_ratio, max_err_estimate_ratio,
       mean_err_estimate_ratio, stddev_err_estimate_ratio,
       min_err_estimate_num, max_err_estimate_num,
       mean_err_estimate_num, stddev_err_estimate_num,
       constant_position, eval_type
FROM pg_qualstats
WHERE dbid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
ORDER BY nbfiltered DESC NULLS LAST, execution_count DESC NULLS LAST,
         queryid, userid, uniquequalid, uniquequalnodeid,
         qualid, qualnodeid, lrelid, lattnum, opno, rrelid, rattnum,
         constant_position, eval_type
LIMIT 1000;
