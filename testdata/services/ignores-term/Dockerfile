FROM golang:1.25-alpine AS build
WORKDIR /src
COPY fixture/go.mod fixture/main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fixture .

FROM scratch
COPY --from=build /out/fixture /fixture
ENV DRAINCHECK_FIXTURE_MODE=ignore
EXPOSE 8080
ENTRYPOINT ["/fixture"]
