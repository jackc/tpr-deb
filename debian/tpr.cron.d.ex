#
# Regular cron jobs for the tpr package
#
0 4	* * *	root	[ -x /usr/bin/tpr_maintenance ] && /usr/bin/tpr_maintenance
