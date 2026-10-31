FROM postgres:16.9 AS builder

ENV PG_CONFIG=/usr/lib/postgresql/16/bin/pg_config

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        autoconf \
        automake \
        libtool \
        pkg-config \
        postgresql-server-dev-16 \
    && "$PG_CONFIG" --version \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL --retry 3 \
        https://codeload.github.com/hightman/scws/tar.gz/refs/tags/1.2.3 \
        -o /tmp/scws.tar.gz \
    && mkdir /tmp/scws \
    && tar -xzf /tmp/scws.tar.gz -C /tmp/scws --strip-components=1 \
    && cd /tmp/scws \
    && touch README \
    && aclocal \
    && autoconf \
    && autoheader \
    && libtoolize --force \
    && automake --add-missing \
    && ./configure --prefix=/usr/local \
    && make -j2 \
    && make install \
    && ldconfig \
    && test -f /usr/local/lib/libscws.so \
    && rm -rf /tmp/scws /tmp/scws.tar.gz
# 固定源码提交，便于以后复现构建
ARG ZHPARSER_REF=2e995c4df672563992b4d7a147b8fa2d0d4cda6c

RUN curl -fsSL --retry 3 \
        "https://codeload.github.com/amutu/zhparser/tar.gz/${ZHPARSER_REF}" \
        -o /tmp/zhparser.tar.gz \
    && mkdir /tmp/zhparser \
    && tar -xzf /tmp/zhparser.tar.gz -C /tmp/zhparser --strip-components=1 \
    && cd /tmp/zhparser \
    && make -j2 PG_CONFIG="$PG_CONFIG" SCWS_HOME=/usr/local \
    && make PG_CONFIG="$PG_CONFIG" SCWS_HOME=/usr/local install \
    && test -f "$("$PG_CONFIG" --pkglibdir)/zhparser.so" \
    && test -f "$("$PG_CONFIG" --sharedir)/extension/zhparser.control" \
    && rm -rf /tmp/zhparser /tmp/zhparser.tar.gz

FROM postgres:16.9 AS runtime

COPY --from=builder /usr/local/lib/libscws.so* /usr/local/lib/
COPY --from=builder /usr/lib/postgresql/16/lib/zhparser.so /usr/lib/postgresql/16/lib/
COPY --from=builder /usr/share/postgresql/16/extension/zhparser* /usr/share/postgresql/16/extension/
COPY --from=builder /usr/share/postgresql/16/tsearch_data/dict.utf8.xdb /usr/share/postgresql/16/tsearch_data/
COPY --from=builder /usr/share/postgresql/16/tsearch_data/rules.utf8.ini /usr/share/postgresql/16/tsearch_data/

RUN ldconfig
