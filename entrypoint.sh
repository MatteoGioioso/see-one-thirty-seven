#!/usr/bin/env sh

PW_FILE=$PGPASSWDDIR/pw
PATH=/usr/local/pgsql/bin:$PATH
PATH=/usr/lib/postgresql/14/bin:$PATH
export PATH

$PGEXTRA/seeone
