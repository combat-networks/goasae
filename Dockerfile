FROM golang:alpine AS builder

ARG branch
ARG commit

WORKDIR /build
COPY . .
RUN go mod tidy && go build -o dist/ -ldflags "-s -X main.gitRevision=$commit -X main.gitBranch=$branch" ./cmd/...

FROM alpine

EXPOSE 8080/tcp
EXPOSE 8088/tcp
EXPOSE 8446/tcp
EXPOSE 8999/tcp
EXPOSE 8999/udp

WORKDIR /app
COPY --from=builder /build/dist/goasae_server /app/goasae_server
COPY ./data /app/data
COPY ./goasae_server.yml /app/
COPY ./users.yml /app/
CMD ["./goasae_server"]