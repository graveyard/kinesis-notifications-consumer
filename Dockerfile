FROM gliderlabs/alpine:3.3

RUN apk-install ca-certificates

COPY kinesis-notifications-consumer /bin/kinesis-notifications-consumer

ENTRYPOINT ["/bin/kinesis-notifications-consumer"]
