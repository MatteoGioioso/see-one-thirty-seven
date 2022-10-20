FROM ubuntu:20.04

ENV PGVERSION=14
ENV PGDATA=/usr/local/pgsql/data
ENV PGEXTRA=/postgres
ENV PGBACKUP=$PGEXTRA/backups
ENV PGPASSWDDIR=$PGEXTRA/password
ENV PGLOGS=$PGEXTRA/logs

ENV ETCD_VER=v3.5.5
ENV DOWNLOAD_URL=https://github.com/etcd-io/etcd/releases/download

RUN adduser \
     --system \
     --shell /bin/bash \
     --gecos "Postgres user" \
     --group \
     --disabled-password \
     --home /home/postgres \
     postgres

# Install all base packages
RUN export DEBIAN_FRONTEND=noninteractive \
    && echo 'APT::Install-Recommends "0";\nAPT::Install-Suggests "0";' > /etc/apt/apt.conf.d/01norecommend \
		&& apt-get update && apt-get upgrade -y && apt-get install wget gnupg lsb-release ca-certificates postgresql-common curl -y

# Install etcd
RUN rm -f /tmp/etcd-${ETCD_VER}-linux-amd64.tar.gz \
    && rm -rf /tmp/etcd-download-test && mkdir -p /tmp/etcd-download-test \
    && curl -L ${DOWNLOAD_URL}/${ETCD_VER}/etcd-${ETCD_VER}-linux-amd64.tar.gz -o /tmp/etcd-${ETCD_VER}-linux-amd64.tar.gz \
    && tar xzvf /tmp/etcd-${ETCD_VER}-linux-amd64.tar.gz -C /tmp/etcd-download-test --strip-components=1 \
    && rm -f /tmp/etcd-${ETCD_VER}-linux-amd64.tar.gz \
    && /tmp/etcd-download-test/etcd --version \
    && /tmp/etcd-download-test/etcdctl version

# Install postgres binary from apt
RUN export DEBIAN_FRONTEND=noninteractive \
		&& sh -c 'echo "deb http://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" > /etc/apt/sources.list.d/pgdg.list' \
    && wget --quiet -O - https://www.postgresql.org/media/keys/ACCC4CF8.asc | apt-key add - \
    && apt-get update \
    && apt-get install postgresql-${PGVERSION} -y

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
