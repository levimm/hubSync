FROM iron/go:dev
WORKDIR /go/src/hubSync
ADD . /go/src/hubSync
RUN CGO_ENABLED=0 go build -o myapp

FROM dind:latest
WORKDIR /app
COPY --from=0 /go/src/hubSync/myapp .
ADD ./start.sh ./start.sh
RUN chmod 755 ./start.sh
CMD ["/bin/bash", "./start.sh"]