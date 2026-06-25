FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app ./cmd/tesseract

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /app /app
EXPOSE 7600 8081
ENTRYPOINT ["/app"]
