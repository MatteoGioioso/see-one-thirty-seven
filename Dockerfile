FROM ubuntu:20.04

ENV PGDATA=/usr/local/pgsql/data
ENV PGEXTRA=/postgres
ENV PGBACKUP=$PGEXTRA/backups
ENV PGPASSWDDIR=$PGEXTRA/password
ENV PGLOGS=$PGEXTRA/logs

RUN adduser \
     --system \
     --shell /bin/bash \
     --gecos "Postgres user" \
     --group \
     --disabled-password \
     --home /home/postgres \
     postgres

# Install all base packages needed for postgres
RUN export DEBIAN_FRONTEND=noninteractive \
    && echo 'APT::Install-Recommends "0";\nAPT::Install-Suggests "0";' > /etc/apt/apt.conf.d/01norecommend \
		&& apt-get update && apt-get upgrade -y && apt-get install wget gnupg lsb-release ca-certificates postgresql-common -y

# Install postgres binary from apt
RUN export DEBIAN_FRONTEND=noninteractive \
		&& sh -c 'echo "deb http://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" > /etc/apt/sources.list.d/pgdg.list' \
    && wget --quiet -O - https://www.postgresql.org/media/keys/ACCC4CF8.asc | apt-key add - \
    && apt-get update \
    && apt-get install postgresql-14 -y

RUN mkdir -p $PGDATA
RUN mkdir -p $PGPASSWDDIR
RUN mkdir -p $PGBACKUP
RUN mkdir -p $PGLOGS

# PGDATA must be owned by postgres user
RUN chown -R postgres $PGDATA $PGEXTRA

COPY entrypoint.sh $PGEXTRA/entrypoint.sh
COPY src/bin/seeone $PGEXTRA/seeone
COPY src/postgresql/postgresql.template.conf $PGEXTRA/postgresql.template.conf
RUN chmod +x $PGEXTRA/entrypoint.sh
RUN chmod +x $PGEXTRA/seeone

USER postgres

CMD $PGEXTRA/entrypoint.sh
