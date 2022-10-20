#!/usr/bin/env sh

PW_FILE=$PGPASSWDDIR/pw
PATH=/usr/local/pgsql/bin:$PATH
PATH=/usr/lib/postgresql/14/bin:$PATH
export PATH

echo "$PGPASSWORD" > "$PW_FILE"

# Clean up data dir before initdb
rm -rf $PGDATA/{*,.*}

if [ "$SEEONE_ROLE" = "master" ]; then
    pg_ctl -D "$PGDATA" initdb -o "--pwfile $PW_FILE --username $PGUSER --auth-host scram-sha-256"
fi

rm $PW_FILE

$PGEXTRA/seeone --role $SEEONE_ROLE
