FROM golang:1.26.1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /life-api ./cmd/life-api
FROM gcr.io/distroless/static:nonroot
COPY --from=build /life-api /life-api
ENV LISTEN_ADDR=0.0.0.0:8093
EXPOSE 8093
USER 65532
ENTRYPOINT ["/life-api"]
