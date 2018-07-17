#!/bin/bash -e

# BusyBox v1.22.1 (Debian 1:1.22.0-9+deb8u1) multi-call binary.

# Usage: syslogd [OPTIONS]

# System logging utility
# (this version of syslogd ignores /etc/syslog.conf)

# 	-n		Run in foreground
# 	-O FILE		Log to FILE (default:/var/log/messages)
# 	-l N		Log only messages more urgent than prio N (1-8)
# 	-S		Smaller output
# 	-R HOST[:PORT]	Log to HOST:PORT (default PORT:514)
# 	-L		Log locally and via network (default is network only if -R)
# 	-C[size_kb]	Log to shared mem buffer (use logread to read it)

: ${REMOTE_LOGGING_IP:=netconsoled}
: ${REMOTE_LOGGING_PORT:=6666}

exec /sbin/syslogd -n -R $REMOTE_LOGGING_IP:$REMOTE_LOGGING_PORT -L -O /dev/stdout
