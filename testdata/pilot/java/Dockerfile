FROM eclipse-temurin:21-jdk-alpine AS build

WORKDIR /src
COPY PilotService.java ./PilotService.java
RUN mkdir /out && javac --add-modules jdk.httpserver -d /out PilotService.java

FROM eclipse-temurin:21-jre-alpine
WORKDIR /app
COPY --from=build /out/ ./

EXPOSE 8080
ENTRYPOINT ["java", "--add-modules", "jdk.httpserver", "-cp", "/app", "PilotService"]
