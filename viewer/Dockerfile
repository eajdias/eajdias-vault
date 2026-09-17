FROM golang:1.25-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0

COPY go.mod ./
COPY *.go ./
COPY static ./static
RUN go mod tidy \
 && go build -trimpath -ldflags="-s -w" -o /out/sankviewer .

FROM alpine:3.20
RUN apk add --no-cache ffmpeg ca-certificates \
 && adduser -D -H viewer \
 && mkdir -p /data/thumbs \
 && chown viewer /data/thumbs

COPY --from=build /out/sankviewer /usr/local/bin/sankviewer

USER viewer
EXPOSE 8080
ENTRYPOINT ["sankviewer"]
