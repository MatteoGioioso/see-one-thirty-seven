FROM postgres:13

COPY bin/bench /bench
RUN chmod +x /bench

ENTRYPOINT ["/bench"]