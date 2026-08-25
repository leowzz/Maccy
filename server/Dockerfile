FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/maccy-server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /out/maccy-server /usr/local/bin/maccy-server
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/maccy-server"]
