FROM golang:1.25.13-alpine AS build
WORKDIR /src
COPY fixture/go.mod fixture/go.sum ./
RUN go mod download
COPY fixture/main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fixture .

FROM scratch
COPY --from=build /out/fixture /fixture
EXPOSE 8080 50051
ENTRYPOINT ["/fixture"]
