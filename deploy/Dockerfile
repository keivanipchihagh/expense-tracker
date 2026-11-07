# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/expense-tracker .

FROM gcr.io/distroless/static-debian13:nonroot

ARG APP_VERSION=dev
ENV EXPENSE_TRACKER_APP_VERSION=${APP_VERSION}

COPY --from=builder /out/expense-tracker /expense-tracker

EXPOSE 8000
ENTRYPOINT ["/expense-tracker"]
