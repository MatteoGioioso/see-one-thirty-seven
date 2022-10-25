#!/usr/bin/env sh

export PGHOST=$PGHOST_1

retry=0
success=false
until [ $retry -ge 10 ]; do
  psql -c '\q' && success=true

  if [ "$success" = true ]; then
    echo "Postgres is available."
    break
  fi

  echo "Postgres is unavailable ($retry) - sleeping..."
  retry=$((retry+1))
  sleep 5
done

# pgbench will write data into the db, however if the host we are connecting is a replica
# this operation will fail (because you cannot write on a read replica).
# We first check on db_one.
# The output of that commands for some reason leaves a white space before the letter
is_replica=$(psql "host=$PGHOST_1 user=postgres port=5432 dbname=postgres" -c "select pg_is_in_recovery()" -t)
if [ "$is_replica" = " f" ]; then
  echo "$PGHOST is the master"
else
  export PGHOST=$PGHOST_2
  echo "$PGHOST is the master"
fi

sleep 5
pgbench -i -s 1 postgres
sleep 10
pgbench -c 8 -T 3600 -s 5 postgres # Run for long enough
