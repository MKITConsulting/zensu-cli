FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && apk upgrade --no-cache \
    && addgroup -S zensu \
    && adduser -S -G zensu -h /nonexistent -s /sbin/nologin zensu
COPY zensu /usr/local/bin/zensu
ENV ZENSU_INSTALL_METHOD=docker
USER zensu:zensu
ENTRYPOINT ["/usr/local/bin/zensu"]
