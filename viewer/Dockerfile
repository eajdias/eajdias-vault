FROM golang:1.25-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0

COPY go.mod ./
COPY *.go ./
COPY static ./static
RUN go mod tidy \
 && go build -trimpath -ldflags="-s -w" -o /out/zubviewer .

FROM alpine:3.20
RUN apk add --no-cache ffmpeg ca-certificates \
 && addgroup -g 2000 vault \
 && adduser -D -H -u 20000 viewer \
 && addgroup viewer vault \
 && mkdir -p /data/thumbs \
 && chown viewer /data/thumbs

COPY --from=build /out/zubviewer /usr/local/bin/zubviewer

USER viewer
EXPOSE 8080
ENTRYPOINT ["zubviewer"]
