FROM golang:1.25.13-alpine AS build
WORKDIR /src
COPY fixture/go.mod fixture/go.sum ./
RUN go mod download
COPY fixture/main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fixture .

FROM alpine:3.23
COPY --from=build /out/fixture /fixture
COPY nonforwarding-pid1/entrypoint.sh /entrypoint.sh
EXPOSE 8080
ENTRYPOINT ["/bin/sh", "/entrypoint.sh"]
