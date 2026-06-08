FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && apk upgrade --no-cache \
    && addgroup -S zensu \
    && adduser -S -G zensu -h /nonexistent -s /sbin/nologin zensu
COPY zensu /usr/local/bin/zensu
USER zensu:zensu
ENTRYPOINT ["/usr/local/bin/zensu"]
