FROM alpine:latest
ADD main.go /app/
ENTRYPOINT [ "ps" ]