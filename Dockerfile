# syntax=docker/dockerfile:1

# builder
FROM golang:alpine AS builder

WORKDIR /src

COPY ./src/go.mod ./src/go.sum ./
RUN go mod download

COPY ./src/*.go /src

RUN CGO_ENABLED=0 GOOS=linux go build -o /cb-autorecover

CMD ["/cb-autorecover"]

# runner
FROM scratch

WORKDIR /src

COPY --from=builder /cb-autorecover /cb-autorecover

CMD [ "/cb-autorecover" ]