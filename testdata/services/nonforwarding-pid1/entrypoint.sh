#!/bin/sh

# This intentionally broken PID 1 keeps SIGTERM instead of forwarding it to
# the service. It models wrappers that launch a child without exec or a trap.
trap '' TERM INT
/fixture &
child="$!"
wait "$child"
