FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/limesurvey-redirector ./cmd/server

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/limesurvey-redirector /app/limesurvey-redirector
ENV APP_ADDR=:8099
ENV DATABASE_PATH=/app/data/redirector.db
EXPOSE 8099
ENTRYPOINT ["/app/limesurvey-redirector"]
