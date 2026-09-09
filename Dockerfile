# croptop host in a container (Railway, Fly, any Docker host).
FROM golang:alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /croptop ./cmd/croptop

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /croptop /usr/local/bin/croptop
VOLUME /data
ENV CROPTOP_DOMAIN=crop.top CROPTOP_ROOT= CROPTOP_ANNOUNCE=
EXPOSE 8090 4001
# HTTP on 8090 for the proxy in front; 4001 is the swarm port to expose as raw TCP.
CMD ["sh", "-c", "exec croptop host --domain \"$CROPTOP_DOMAIN\" --root \"$CROPTOP_ROOT\" --announce \"$CROPTOP_ANNOUNCE\" --listen 0.0.0.0:8090 --data /data"]
